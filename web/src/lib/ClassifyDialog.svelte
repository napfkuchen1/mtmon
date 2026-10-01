<script>
  import { api } from './api.js'
  import { toast } from './state.svelte.js'
  import { bytes, num } from './format.js'
  import Modal from './Modal.svelte'
  // init: { kind, value, proto, name, hint }
  let { init, onclose, onsaved, known = [] } = $props()
  let kind = $state(init.kind || 'port'), value = $state(init.value || ''), proto = $state(init.proto || 0)
  let name = $state(init.name || ''), category = $state(init.category || ''), err = $state(''), busy = $state(false)
  let prev = $state(null)
  const kinds = [
    ['port', 'Port', '443 oder 123'], ['ip', 'Eine IP-Adresse', '203.0.113.7'], ['cidr', 'Netz (CIDR)', '17.0.0.0/8'],
    ['host', 'Hostname (inkl. Subdomains)', 'bambulab.com'], ['asn', 'AS-Nummer (Anbieter)', '13335']
  ]
  const hint = $derived(kinds.find(k => k[0] === kind)?.[2])
  $effect(() => {
    const body = { name: name || 'x', kind, value, proto: kind === 'port' ? +proto : 0 }
    prev = null
    if (!value) return
    const t = setTimeout(async () => { try { prev = await api('/service-rules/preview', { method: 'POST', body }); err = '' } catch (e) { prev = null; err = e.message } }, 300)
    return () => clearTimeout(t)
  })
  async function save() {
    busy = true
    try {
      await api('/service-rules', { method: 'POST', body: { name, category, kind, value, proto: kind === 'port' ? +proto : 0 } })
      toast('Service-Regel gespeichert – alte Daten werden im Hintergrund neu zugeordnet')
      onsaved?.(); onclose()
    } catch (e) { err = e.message } finally { busy = false }
  }
</script>
<Modal title="Als Service klassifizieren" {onclose}>
  {#if init.hint}<p class="muted" style="margin-top:0">{init.hint}</p>{/if}
  <div class="form">
    <label>Service-Name<input class="input" bind:value={name} list="svcnames" maxlength="40" placeholder="z. B. Bambu Cloud" /></label>
    <datalist id="svcnames">{#each known as k}<option value={k}></option>{/each}</datalist>
    <label>Kategorie<input class="input" bind:value={category} maxlength="30" placeholder="optional, z. B. 3D-Drucker" /></label>
    <label>Zuordnen nach
      <select class="input" bind:value={kind}>{#each kinds as [id, l]}<option value={id}>{l}</option>{/each}</select></label>
    <label>Wert<input class="input mono" bind:value={value} placeholder={hint} /></label>
    {#if kind === 'port'}
      <label>Protokoll<select class="input" bind:value={proto}><option value={0}>TCP und UDP</option><option value={6}>nur TCP</option><option value={17}>nur UDP</option></select></label>
    {/if}
  </div>
  {#if prev}
    <div class="prev">Betrifft in den letzten 30 Tagen: <b>{num(prev.dests)}</b> Ziele · <b>{num(prev.clients)}</b> Client(s) · <b>{bytes(prev.bytes)}</b></div>
  {/if}
  <p class="muted" style="font-size:12.5px;margin-bottom:0">Stärkere Regeln gewinnen: IP &gt; Netz &gt; Hostname &gt; AS &gt; Port &gt; Standardname. Die Regel gilt sofort für neue Verbindungen und wird auf bereits gespeicherte Daten angewendet.</p>
  {#if err}<div class="warnbox" style="margin-top:12px">{err}</div>{/if}
  {#snippet footer()}
    <button class="btn" onclick={onclose}>Abbrechen</button>
    <button class="btn primary" disabled={busy || !name || !value} onclick={save}>Speichern</button>
  {/snippet}
</Modal>
<style>
  .form { display: grid; gap: 12px; } label { display: grid; gap: 4px; font-weight: 500; font-size: 13px; }
  .prev { margin-top: 14px; padding: 10px 12px; background: var(--accent-bg); border-radius: 8px; font-size: 13px; }
</style>
