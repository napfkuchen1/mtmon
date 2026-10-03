<script>
  import { api } from './api.js'
  import { go } from './state.svelte.js'
  import { t } from './i18n.svelte.js'
  import Modal from './Modal.svelte'
  let { onclose } = $props()
  let q = $state(''), idx = $state(null), cur = $state(0), err = $state('')
  const pages = [['overview', 'Overview'], ['live', 'Live'], ['devices', 'Devices'], ['clients', 'Clients'], ['inbox', 'New devices'], ['services', 'Services'], ['firewall', 'Firewall'],
    ['insights', 'Insights'], ['suggestions', 'Suggestions'], ['topology', 'Topology'], ['alerts', 'Alerts'], ['settings', 'Settings']]
  api('/search/index').then(r => (idx = r)).catch(e => (err = e.message))

  const results = $derived.by(() => {
    const s = q.trim().toLowerCase()
    const has = (...v) => !s || v.join(' ').toLowerCase().includes(s)
    const out = []
    for (const [id, l] of pages) if (has(t(l), id)) out.push({ k: t('Page'), name: t(l), sub: '', to: id })
    if (s && idx) {
      for (const c of idx.clients) if (has(c.name, c.ip, c.mac, c.vendor)) out.push({ k: t('Client'), name: c.name, sub: [c.ip, c.mac].filter(Boolean).join(' · '), on: c.online, to: 'client/' + encodeURIComponent(c.mac) })
      for (const d of idx.devices) if (has(d.name, d.addr)) out.push({ k: t('Device'), name: d.name, sub: d.addr, to: 'device/' + encodeURIComponent(d.name) })
      for (const v of idx.services) if (has(v)) out.push({ k: t('Service'), name: v, sub: '', to: 'services/' + encodeURIComponent(v) })
    }
    return out.slice(0, 40)
  })
  $effect(() => { q; cur = 0 })
  function pick(r) { go(r.to); onclose() }
  function key(e) {
    if (e.key === 'ArrowDown') { cur = Math.min(cur + 1, results.length - 1); e.preventDefault() }
    else if (e.key === 'ArrowUp') { cur = Math.max(cur - 1, 0); e.preventDefault() }
    else if (e.key === 'Enter' && results[cur]) pick(results[cur])
  }
</script>
<Modal title={t('Search')} {onclose}>
  <!-- svelte-ignore a11y_autofocus -->
  <input class="input" style="width:100%" autofocus placeholder={t('Search clients, devices, services, pages…')} bind:value={q} onkeydown={key} aria-label={t('Search')} />
  {#if err}<div class="warnbox" style="margin-top:10px">{err}</div>{/if}
  <ul>
    {#each results as r, i}
      <li><button class:on={i === cur} onclick={() => pick(r)} onmouseenter={() => (cur = i)}>
        <span class="k">{r.k}</span>{#if r.on !== undefined}<span class="dot" class:ok={r.on}></span>{/if}<b>{r.name}</b><span class="muted sub">{r.sub}</span></button></li>
    {/each}
  </ul>
  {#if q && !results.length}<div class="empty">{t('Nothing found')}</div>{/if}
</Modal>
<style>
  ul { list-style: none; margin: 10px 0 0; padding: 0; max-height: 50vh; overflow-y: auto; }
  li button { display: flex; gap: 10px; align-items: center; width: 100%; text-align: left; border: 0; background: none; color: var(--text); padding: 7px 10px; border-radius: 8px; cursor: pointer; font: inherit; }
  li button.on { background: var(--accent-bg); }
  .k { font-size: 11px; text-transform: uppercase; letter-spacing: .05em; color: var(--muted); min-width: 56px; }
  .sub { margin-left: auto; font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
