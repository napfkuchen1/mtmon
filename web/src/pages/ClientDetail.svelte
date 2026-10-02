<script>
  import { api, download } from '../lib/api.js'
  import { app, toast } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { bps, bytes, datetime, clock, display, protoName, flag, signalQuality } from '../lib/format.js'
  import { t, tr } from '../lib/i18n.svelte.js'
  import Chart from '../lib/Chart.svelte'
  import TopList from '../lib/TopList.svelte'
  import ClassifyDialog from '../lib/ClassifyDialog.svelte'
  import AppList from '../lib/AppList.svelte'
  import IPName from '../lib/IPName.svelte'
  import { go } from '../lib/state.svelte.js'
  let dlg = $state(null)
  const blocked = poll(() => api('/clients/' + encodeURIComponent(app.route.params.id) + '/firewall?verdict=blocked&range=' + (app.range === 'live' ? '1h' : app.range)), 8000)
  const apps = poll(() => api('/clients/' + encodeURIComponent(app.route.params.id) + '/apps?range=' + (app.range === 'live' ? '1h' : app.range)), 6000)
  const vc = { blocked: 'bad', allowed: 'ok', logged: '' }
  const vt = $derived({ blocked: t('blocked'), allowed: t('allowed'), logged: t('logged') })

  const mac = $derived(app.route.params.id)
  const rg = () => (app.range === 'live' ? '1h' : app.range)
  const d = poll(() => api('/clients/' + encodeURIComponent(app.route.params.id) + '?range=' + rg()), 5000)
  const x = $derived(d.data)
  const c = $derived(x?.client)

  // connection timeline with live tail
  let conns = $state([]), tail = $state(true), filter = $state(''), dirF = $state('all'), cerr = $state('')
  $effect(() => {
    const id = app.route.params.id, range = rg()
    let stop = false, t, after = 0
    conns = []
    const run = async () => {
      try {
        const rows = await api(`/clients/${encodeURIComponent(id)}/connections?range=${range}&limit=300&after=${after}`)
        if (rows.length) { after = Math.max(after, ...rows.map(r => r.ts)); conns = [...rows, ...conns].slice(0, 1500) }
        cerr = ''
      } catch (e) { cerr = e.message }
      if (!stop && tail) t = setTimeout(run, 3000)
    }
    if (tail || !conns.length) run()
    return () => { stop = true; clearTimeout(t) }
  })
  const shown = $derived(conns.filter(r => (dirF === 'all' || r.dir === dirF) &&
    (!filter || (r.rip + r.host + r.svc + r.rport + r.cc + r.asorg).toLowerCase().includes(filter.toLowerCase()))))

  // Series buckets differ by range; convert using bucket size.
  const bucket = $derived({ '1h': 60, '24h': 900, '7d': 3600, '30d': 14400 }[rg()] || 60)
  const chart = $derived([
    { name: t('Download'), color: 'var(--down)', points: (x?.series || []).map(p => ({ x: p.ts, y: (p.down * 8) / bucket })) },
    { name: t('Upload'), color: 'var(--up)', points: (x?.series || []).map(p => ({ x: p.ts, y: (p.up * 8) / bucket })) }
  ])
  const liveRate = $derived(app.live.clients.find(v => v.key === mac))
  let editing = $state(false), label = $state('')
  async function saveLabel() {
    try { await api('/clients/' + encodeURIComponent(mac) + '/label', { method: 'PUT', body: { label } }); editing = false; toast(t('Saved')); d.data = await api('/clients/' + encodeURIComponent(mac) + '?range=' + rg()) }
    catch (e) { toast(tr(e.message)) }
  }
  const arrow = $derived({ u: t('↑ out'), d: t('↓ in'), i: t('⇄ lan') })
  const sq = $derived(c ? signalQuality(c.signal) : {})
</script>

<div class="head"><a href="#/clients">{t('← Clients')}</a></div>
{#if d.error}<div class="warnbox">{tr(d.error)}</div>{/if}

{#if c}
  <div class="title">
    <span class="dot" class:ok={c.online} class:bad={!c.online}></span>
    {#if editing}
      <input class="input" bind:value={label} maxlength="64" aria-label={t('Client label')} onkeydown={e => e.key === 'Enter' && saveLabel()} />
      <button class="btn sm primary" onclick={saveLabel}>{t('Save')}</button><button class="btn sm" onclick={() => (editing = false)}>{t('Cancel')}</button>
    {:else}
      <h1>{display(c)}</h1>
      <button class="btn sm" onclick={() => { label = c.label || c.hostname; editing = true }}>{t('Rename')}</button>
    {/if}
    <span class="badge {c.online ? 'ok' : ''}">{c.online ? t('online') : t('offline')}</span>
    {#if c.wifi}<span class="badge acc">{t('Wi-Fi')} {c.ssid}{c.band ? ' · ' + c.band : ''}</span>{:else}<span class="badge">{t('wired')}</span>{/if}
    {#if c.vendor}<span class="badge">{tr(c.vendor)}</span>{/if}
  </div>

  <div class="grid g4" style="margin:16px 0">
    <div class="card card-b kpi"><div class="l">{t('Address')}</div><div class="v mono" style="font-size:17px">{c.ip || '—'}</div><div class="l mono">{c.mac}</div></div>
    <div class="card card-b kpi"><div class="l">{t('Connected to')}</div><div class="v" style="font-size:17px">{c.device || '—'}</div><div class="l">{c.wifi ? (c.signal ? sq.txt + ' · ' : '') + (c.tx_rate || '') : c.iface}</div></div>
    <div class="card card-b kpi"><div class="l">{t('Right now')}</div><div class="v num" style="font-size:17px">↓ {bps(liveRate?.down_bps)} <span class="muted">↑ {bps(liveRate?.up_bps)}</span></div><div class="l">{t('30 s average')}</div></div>
    <div class="card card-b kpi"><div class="l">{t('Seen')}</div><div class="v" style="font-size:17px">{datetime(c.last_seen)}</div><div class="l">{t('first {date}', { date: datetime(c.first_seen) })}</div></div>
  </div>

  {#if x.new_destinations?.length}
    <div class="warnbox" style="margin-bottom:16px"><b>{x.new_destinations.length > 1 ? t('{n} destinations contacted for the first time in the last 24 h:', { n: x.new_destinations.length }) : t('1 destination contacted for the first time in the last 24 h:')}</b>
      <span class="mono"> {x.new_destinations.slice(0, 8).join(', ')}{x.new_destinations.length > 8 ? ' …' : ''}</span></div>
  {/if}

  <div class="card"><div class="card-h"><h2>{t('Traffic')}</h2><div class="legend"><span><i style="background:var(--down)"></i>{t('Download')}</span><span><i style="background:var(--up)"></i>{t('Upload')}</span></div></div>
    <div class="card-b"><Chart series={chart} fmt={bps} height={180} /></div></div>

  <div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('Apps')}</h2><span class="muted" style="font-size:12.5px">{t('Detected from DNS names of the destinations')}</span></div>
    {#if apps.error}<div class="warnbox" style="margin:12px">{tr(apps.error)}</div>{/if}
    <AppList apps={apps.data?.apps || []} unknown={apps.data?.unknown_bytes || 0} known={apps.data?.known_bytes || 0} truncated={apps.data?.truncated} empty={apps.loading ? t('Loading…') : null} /></div>

  <div class="grid g2" style="margin-top:16px">
    <div class="card"><div class="card-h"><h2>{t('Top destinations')}</h2></div><TopList rows={x.top_hosts} /></div>
    <div class="card"><div class="card-h"><h2>{t('Top services')}</h2><span class="muted" style="font-size:12.5px">{t('Click: who else uses this?')}</span></div><TopList rows={x.top_services} onpick={r => go('services/' + encodeURIComponent(r.key))} /></div>
    <div class="card"><div class="card-h"><h2>{t('Top ports')}</h2></div><TopList rows={x.top_ports} /></div>
    <div class="card"><div class="card-h"><h2>{t('Countries')}</h2></div><TopList rows={x.top_countries} mode="country" empty={t('No GeoIP database configured')} /></div>
  </div>

  <div class="card" style="margin-top:16px">
    <div class="card-h"><h2>{t('Connections')}</h2>
      <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center">
        <input class="input" placeholder={t('Filter IP, host, port, service…')} bind:value={filter} aria-label={t('Filter')} />
        <div class="tabs">{#each [['all', t('All')], ['u', t('↑ Out')], ['d', t('↓ In')], ['i', t('⇄ LAN')]] as [id, l]}<button class:on={dirF === id} onclick={() => (dirF = id)}>{l}</button>{/each}</div>
        <button class="btn" onclick={() => (tail = !tail)}>{tail ? t('Pause live tail') : t('Resume live tail')}</button>
        <button class="btn" onclick={() => download('/export/connections/' + encodeURIComponent(mac) + '?range=' + rg())}>{t('CSV')}</button>
      </div></div>
    <div class="scroll" style="max-height:520px;overflow-y:auto">
      <table>
        <thead><tr><th>{t('Time')}</th><th>{t('Dir')}</th><th>{t('Destination')}</th><th>{t('Ports (src → dst)')}</th><th>{t('Service')}</th><th>{t('Firewall')}</th><th>{t('Via')}</th><th>{t('Country / AS')}</th><th class="r">{t('Bytes')}</th><th></th></tr></thead>
        <tbody>
          {#each shown.slice(0, 400) as r (r.ts + r.rip + r.rport + r.cport + r.bytes + r.dir)}
            <tr><td class="mono muted">{clock(r.ts)}</td><td class:dn={r.dir === 'd'} class:upc={r.dir === 'u'}>{arrow[r.dir]}</td>
              <td><span class="mono">{r.rip}</span>{#if r.host}<div class="muted">{r.host}</div>{/if}</td>
              <td class="mono">{protoName(r.proto)} {r.dir === 'd' ? r.rport + ' → ' + r.cport : r.cport + ' → ' + r.rport}</td>
              <td>{#if r.svc}<a class="badge" href="#/services/{encodeURIComponent(r.svc)}">{tr(r.svc)}</a>{/if}</td>
              <td>{#if r.verdict}<span class="badge {vc[r.verdict]}" title={tr(r.rule)}>{vt[r.verdict]}</span>{:else}<span class="muted" title={t('No firewall logging active for this router')}>–</span>{/if}</td>
              <td class="muted">{r.device || ''}</td>
              <td>{flag(r.cc)} {r.cc} <span class="muted">{r.asorg}</span></td><td class="r num">{bytes(r.bytes)}</td>
              <td class="r"><button class="btn sm" title={t('Classify as service')} onclick={() => (dlg = { kind: r.host ? 'host' : 'ip', value: r.host ? r.host.split('.').slice(-2).join('.') : r.rip, name: r.svc || '', hint: t('Connection to {target}', { target: (r.host || r.rip) + ':' + r.rport }) })}>＋</button></td></tr>
          {/each}
        </tbody></table>
      {#if !shown.length}<div class="empty">{cerr ? tr(cerr) : t('No connections recorded in this range')}</div>{/if}
    </div>
  </div>

  {#if blocked.data?.length}
    <div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('Blocked connection attempts')}</h2><span class="muted" style="font-size:12.5px">{t('dropped by the firewall – not visible in flows')}</span></div>
      <div class="scroll" style="max-height:300px;overflow-y:auto"><table><thead><tr><th>{t('Time')}</th><th>{t('Target')}</th><th>{t('Proto')}</th><th>{t('Rule')}</th><th>{t('Router')}</th></tr></thead>
        <tbody>{#each blocked.data as e, i (e.ts + e.dst + e.dport + i)}<tr><td class="mono muted">{clock(e.ts)}</td><td><IPName ip={e.dst} port={e.dport} name={e.dst_name} org={e.dst_org} cc={e.dst_country} /></td><td class="mono">{e.proto}</td><td>{tr(e.rule)}</td><td class="muted">{e.device}</td></tr>{/each}</tbody></table></div></div>
  {/if}

  <div class="grid g2" style="margin-top:16px">
    <div class="card"><div class="card-h"><h2>{t('IP history')}</h2></div><table><tbody>
      {#each x.ip_history as h}<tr><td class="mono">{h.ip}</td><td class="r muted">{datetime(h.first)} → {datetime(h.last)}</td></tr>{/each}
    </tbody></table>{#if !x.ip_history.length}<div class="empty">—</div>{/if}</div>
    <div class="card"><div class="card-h"><h2>{t('Roaming')}</h2></div><table><tbody>
      {#each x.roams as r}<tr><td>{r.from} → <b>{r.to}</b></td><td class="r muted">{datetime(r.ts)}</td></tr>{/each}
    </tbody></table>{#if !x.roams.length}<div class="empty">{t('No roaming events')}</div>{/if}</div>
  </div>
{/if}
{#if dlg}<ClassifyDialog init={dlg} onclose={() => (dlg = null)} />{/if}

<style>
  .head { margin-bottom: 14px; } .title { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
  .dn { color: var(--down); font-weight: 600; white-space: nowrap; } .upc { color: var(--up); font-weight: 600; white-space: nowrap; }
</style>
