<script>
  import { api } from '../lib/api.js'
  import { app, go, toast } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { bytes, bps, num, flag, protoName, ago, display } from '../lib/format.js'
  import { t, tr } from '../lib/i18n.svelte.js'
  import Chart from '../lib/Chart.svelte'
  import ClassifyDialog from '../lib/ClassifyDialog.svelte'
  const rg = () => (app.range === 'live' ? '1h' : app.range)
  const name = $derived(app.route.params.id)

  const list = poll(() => api('/services?range=' + rg()), 8000)
  const rules = poll(() => api('/service-rules'), 0)
  const det = poll(() => app.route.params.id ? api('/services/' + encodeURIComponent(app.route.params.id) + '?range=' + rg()) : Promise.resolve(null), 6000)

  let q = $state(''), cat = $state(''), dlg = $state(null), showRules = $state(false)
  const services = $derived(list.data?.services || [])
  const cats = $derived([...new Set(services.map(s => s.category))].sort())
  const shown = $derived(services.filter(s => (!cat || s.category === cat) && (!q || s.name.toLowerCase().includes(q.toLowerCase()))))
  const tot = s => s.up + s.down + s.internal
  const max = $derived(shown.reduce((m, s) => Math.max(m, tot(s)), 0) || 1)
  const d = $derived(det.data?.detail)
  const bucket = $derived({ '1h': 60, '24h': 900, '7d': 3600, '30d': 14400 }[rg()] || 60)
  const eff = $derived(rg() === '1h' ? 60 : Math.max(bucket, 3600))
  const chart = $derived([
    { name: t('Download'), color: 'var(--down)', points: (d?.series || []).map(p => ({ x: p.ts, y: (p.down * 8) / eff })) },
    { name: t('Upload'), color: 'var(--up)', points: (d?.series || []).map(p => ({ x: p.ts, y: (p.up * 8) / eff })) }
  ])
  const dsts = $derived((d?.dests || []))
  function classifyDest(x) {
    dlg = { kind: 'ip', value: x.rip, name: '', hint: t('Assign all connections to {target} to a service.', { target: x.host || x.rip }), alt: x }
  }
  function classifyPort(x) {
    dlg = { kind: 'port', value: String(x.port), proto: x.proto === 17 ? 17 : x.proto === 6 ? 6 : 0, name: '', hint: t('Assign everything on {target} to a service.', { target: protoName(x.proto) + '/' + x.port }) }
  }
  async function delRule(r) {
    if (!confirm(t('Delete rule “{name}” ({kind} {value})?', { name: r.name, kind: r.kind, value: r.value }))) return
    try { await api('/service-rules/' + r.id, { method: 'DELETE' }); toast(t('Rule deleted – assignment will be reset')); rules.data = await api('/service-rules') } catch (e) { toast(tr(e.message)) }
  }
  const saved = async () => { rules.data = await api('/service-rules'); list.data = await api('/services?range=' + rg()) }
</script>

{#if !name}
  <div class="head"><h1>{t('Services')}</h1><span class="muted">{t('Which service is used by which devices · last {range}', { range: rg() })}</span>
    <span style="flex:1"></span><button class="btn" onclick={() => (showRules = !showRules)}>{t('Custom rules ({n})', { n: rules.data?.length || 0 })}</button>
    <button class="btn primary" onclick={() => (dlg = { kind: 'port', value: '', name: '', hint: t('Create a new assignment – e.g. port 123/UDP → “NTP”, or a hostname → “Bambu Cloud”.') })}>＋ {t('Define service')}</button></div>
  {#if list.error}<div class="warnbox">{tr(list.error)}</div>{/if}
  {#if list.data?.reclassifying}<div class="warnbox" style="margin-bottom:12px">{t('Old data is being reclassified … the numbers will update shortly.')}</div>{/if}

  {#if showRules}
    <div class="card" style="margin-bottom:16px"><div class="card-h"><h2>{t('Custom classification rules')}</h2><span class="muted" style="font-size:12.5px">{t('IP > network > hostname > AS > port > default name')}</span></div>
      <table><thead><tr><th>{t('Service')}</th><th>{t('Category')}</th><th>{t('Type')}</th><th>{t('Value')}</th><th></th></tr></thead>
        <tbody>{#each rules.data || [] as r (r.id)}<tr><td><b>{r.name}</b></td><td class="muted">{r.category ? tr(r.category) : '—'}</td><td>{r.kind}</td><td class="mono">{r.value}{r.kind === 'port' && r.proto ? '/' + protoName(r.proto) : ''}</td><td class="r"><button class="btn sm" onclick={() => delRule(r)}>{t('Delete')}</button></td></tr>{/each}</tbody></table>
      {#if !rules.data?.length}<div class="empty">{t('No custom rules yet. Click “Classify” on destinations or ports.')}</div>{/if}</div>
  {/if}

  <div class="card">
    <div class="card-h">
      <div class="pill-row"><button class="btn sm" class:primary={!cat} onclick={() => (cat = '')}>{t('All')}</button>
        {#each cats as c}<button class="btn sm" class:primary={cat === c} onclick={() => (cat = c)}>{tr(c)}</button>{/each}</div>
      <input class="input" placeholder={t('Search service …')} bind:value={q} aria-label={t('Search service')} />
    </div>
    <div class="scroll"><table>
      <thead><tr><th>{t('Service')}</th><th>{t('Category')}</th><th class="r">{t('Clients')}</th><th class="r">{t('Destinations')}</th><th class="r">{t('↓ Down')}</th><th class="r">{t('↑ Up')}</th><th style="width:150px">{t('Share')}</th><th class="r">{t('Flows')}</th></tr></thead>
      <tbody>{#each shown as s (s.name)}
        <tr class="click" onclick={() => go('services/' + encodeURIComponent(s.name))}>
          <td><b>{tr(s.name)}</b></td><td><span class="badge">{tr(s.category)}</span></td><td class="r num" title={app.range === '30d' ? t('Clients of the last 48 h') : ''}>{num(s.clients)}</td><td class="r num">{num(s.dests)}</td>
          <td class="r num">{bytes(s.down)}</td><td class="r num">{bytes(s.up)}</td><td><div class="bar"><i style="width:{(tot(s) / max) * 100}%"></i></div></td><td class="r num muted">{num(s.flows)}</td></tr>
      {/each}</tbody></table>
      {#if !shown.length}<div class="empty">{list.loading ? t('Loading…') : t('No services with traffic in this range')}</div>{/if}
    </div>
  </div>

{:else}
  <div class="head"><a href="#/services">{t('← Services')}</a><h1>{tr(name)}</h1>{#if det.data}<span class="badge">{tr(det.data.category)}</span>{/if}</div>
  {#if det.error}<div class="warnbox">{tr(det.error)}</div>{/if}
  {#if d}
    <div class="grid g4" style="margin-bottom:16px">
      <div class="card card-b kpi"><div class="l">{t('Clients')}</div><div class="v num">{d.clients.length}</div><div class="l">{t('use this service')}</div></div>
      <div class="card card-b kpi"><div class="l">{t('Destinations')}</div><div class="v num">{d.dests.length}{d.dests.length >= 50 ? '+' : ''}</div><div class="l">{t('address/port combinations')}</div></div>
      <div class="card card-b kpi"><div class="l">{t('↓ Download')}</div><div class="v num">{bytes(d.down)}</div><div class="l">{t('in {range}', { range: rg() })}</div></div>
      <div class="card card-b kpi"><div class="l">{t('↑ Upload')}</div><div class="v num">{bytes(d.up)}</div><div class="l">{t('{n} flows', { n: num(d.flows) })}</div></div>
    </div>

    <div class="card"><div class="card-h"><h2>{t('Who uses “{name}”?', { name: tr(name) })}</h2><span class="muted" style="font-size:12.5px">{d.clients_since ? t('Clients: last 48 h only (long ranges have no client detail) · ') : ''}{t('Click opens the client')}</span></div>
      <div class="scroll"><table>
        <thead><tr><th>{t('Client')}</th><th>{t('Connected to')}</th><th class="r">{t('Destinations')}</th><th class="r">{t('↓ Down')}</th><th class="r">{t('↑ Up')}</th><th class="r">{t('Flows')}</th></tr></thead>
        <tbody>{#each d.clients as c (c.mac)}
          <tr class="click" onclick={() => go('client/' + encodeURIComponent(c.mac))}>
            <td><b>{c.label || c.mac}</b><div class="muted mono" style="font-size:11.5px">{c.ip} · {c.vendor ? tr(c.vendor) : c.mac}</div></td><td>{c.device || '—'}</td>
            <td class="r num">{num(c.dests)}</td><td class="r num">{bytes(c.down)}</td><td class="r num">{bytes(c.up)}</td><td class="r num muted">{num(c.flows)}</td></tr>
        {/each}</tbody></table>
        {#if !d.clients.length}<div class="empty">{t('No clients in this range')}</div>{/if}</div></div>

    <div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('Traffic')}</h2><div class="legend"><span><i style="background:var(--down)"></i>{t('Download')}</span><span><i style="background:var(--up)"></i>{t('Upload')}</span></div></div>
      <div class="card-b"><Chart series={chart} fmt={bps} height={150} /></div></div>

    <div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('Destinations')}</h2><span class="muted" style="font-size:12.5px">{t('where the traffic goes')}</span></div>
      <div class="scroll"><table>
        <thead><tr><th>{t('Destination')}</th><th>{t('Port')}</th><th>{t('Country / Provider')}</th><th class="r">{t('Clients')}</th><th class="r">{t('↓ Down')}</th><th class="r">{t('↑ Up')}</th><th></th></tr></thead>
        <tbody>{#each dsts as x (x.rip + x.port + x.proto)}
          <tr><td><span class="mono">{x.rip}</span>{#if x.host}<div class="muted">{x.host}</div>{/if}</td><td class="mono">{protoName(x.proto)}/{x.port}</td>
            <td>{flag(x.cc)} {x.cc} <span class="muted">{x.asorg}</span></td><td class="r num">{x.clients < 0 ? '–' : x.clients}</td><td class="r num">{bytes(x.down)}</td><td class="r num">{bytes(x.up)}</td>
            <td class="r" style="white-space:nowrap"><button class="btn sm" onclick={() => classifyDest(x)}>{t('Destination')}</button> <button class="btn sm" onclick={() => classifyPort(x)}>{t('Port')}</button></td></tr>
        {/each}</tbody></table>
        {#if !dsts.length}<div class="empty">{t('No destinations')}</div>{/if}</div></div>
  {/if}
{/if}

{#if dlg}<ClassifyDialog init={dlg} known={services.map(s => s.name)} onclose={() => (dlg = null)} onsaved={saved} />{/if}

<style>
  .head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 18px; flex-wrap: wrap; }
</style>
