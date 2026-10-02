<script>
  import { api } from './api.js'
  import { t, tr } from './i18n.svelte.js'
  import { toast } from './state.svelte.js'
  import { bytes, num } from './format.js'
  import Modal from './Modal.svelte'
  // init: { kind, value, proto, name, hint }
  let { init, onclose, onsaved, known = [] } = $props()
  let kind = $state(init.kind || 'port'), value = $state(init.value || ''), proto = $state(init.proto || 0)
  let name = $state(init.name || ''), category = $state(init.category || ''), err = $state(''), busy = $state(false)
  let prev = $state(null)
  const kinds = $derived([
    ['port', t('Port'), t('443 or 123')], ['ip', t('A single IP address'), '203.0.113.7'], ['cidr', t('Network (CIDR)'), '17.0.0.0/8'],
    ['host', t('Hostname (incl. subdomains)'), 'bambulab.com'], ['asn', t('AS number (provider)'), '13335']
  ])
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
      toast(t('Service rule saved – old data is being re-assigned in the background'))
      onsaved?.(); onclose()
    } catch (e) { err = e.message } finally { busy = false }
  }
</script>
<Modal title={t('Classify as service')} {onclose}>
  {#if init.hint}<p class="muted" style="margin-top:0">{tr(init.hint)}</p>{/if}
  <div class="form">
    <label>{t('Service name')}<input class="input" bind:value={name} list="svcnames" maxlength="40" placeholder={t('e.g. Bambu Cloud')} /></label>
    <datalist id="svcnames">{#each known as k}<option value={k}></option>{/each}</datalist>
    <label>{t('Category')}<input class="input" bind:value={category} maxlength="30" placeholder={t('optional, e.g. 3D printer')} /></label>
    <label>{t('Match by')}
      <select class="input" bind:value={kind}>{#each kinds as [id, l]}<option value={id}>{l}</option>{/each}</select></label>
    <label>{t('Value')}<input class="input mono" bind:value={value} placeholder={hint} /></label>
    {#if kind === 'port'}
      <label>{t('Protocol')}<select class="input" bind:value={proto}><option value={0}>{t('TCP and UDP')}</option><option value={6}>{t('TCP only')}</option><option value={17}>{t('UDP only')}</option></select></label>
    {/if}
  </div>
  {#if prev}
    <div class="prev">{t('Affects in the last 30 days:')} <b>{num(prev.dests)}</b> {t('destinations')} · <b>{num(prev.clients)}</b> {t('client(s)')} · <b>{bytes(prev.bytes)}</b></div>
  {/if}
  <p class="muted" style="font-size:12.5px;margin-bottom:0">{t('Stronger rules win: IP > network > hostname > AS > port > default name. The rule applies immediately to new connections and is applied to already stored data.')}</p>
  {#if err}<div class="warnbox" style="margin-top:12px">{tr(err)}</div>{/if}
  {#snippet footer()}
    <button class="btn" onclick={onclose}>{t('Cancel')}</button>
    <button class="btn primary" disabled={busy || !name || !value} onclick={save}>{t('Save')}</button>
  {/snippet}
</Modal>
<style>
  .form { display: grid; gap: 12px; } label { display: grid; gap: 4px; font-weight: 500; font-size: 13px; }
  .prev { margin-top: 14px; padding: 10px 12px; background: var(--accent-bg); border-radius: 8px; font-size: 13px; }
</style>
