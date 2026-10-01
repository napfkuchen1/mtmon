<script>
  import { api, download } from '../lib/api.js'
  import { app, toast } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { bps, bytes, datetime, clock, display, protoName, flag, signalQuality } from '../lib/format.js'
  import Chart from '../lib/Chart.svelte'
  import TopList from '../lib/TopList.svelte'
  import ClassifyDialog from '../lib/ClassifyDialog.svelte'
  import { go } from '../lib/state.svelte.js'
  let dlg = $state(null)
  const blocked = poll(() => api('/clients/' + encodeURIComponent(app.route.params.id) + '/firewall?verdict=blocked&range=' + (app.range === 'live' ? '1h' : app.range)), 8000)
  const vc = { blocked: 'bad', allowed: 'ok', logged: '' }
  const vt = { blocked: 'blockiert', allowed: 'durchgelassen', logged: 'geloggt' }

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
    { name: 'Download', color: 'var(--down)', points: (x?.series || []).map(p => ({ x: p.ts, y: (p.down * 8) / bucket })) },
    { name: 'Upload', color: 'var(--up)', points: (x?.series || []).map(p => ({ x: p.ts, y: (p.up * 8) / bucket })) }
  ])
  const liveRate = $derived(app.live.clients.find(v => v.key === mac))
  let editing = $state(false), label = $state('')
  async function saveLabel() {
    try { await api('/clients/' + encodeURIComponent(mac) + '/label', { method: 'PUT', body: { label } }); editing = false; toast('Saved'); d.data = await api('/clients/' + encodeURIComponent(mac) + '?range=' + rg()) }
    catch (e) { toast(e.message) }
  }
  const arrow = { u: '↑ out', d: '↓ in', i: '⇄ lan' }
  const sq = $derived(c ? signalQuality(c.signal) : {})
</script>

<div class="head"><a href="#/clients">← Clients</a></div>
{#if d.error}<div class="warnbox">{d.error}</div>{/if}

{#if c}
  <div class="title">
    <span class="dot" class:ok={c.online} class:bad={!c.online}></span>
    {#if editing}
      <input class="input" bind:value={label} maxlength="64" aria-label="Client label" onkeydown={e => e.key === 'Enter' && saveLabel()} />
      <button class="btn sm primary" onclick={saveLabel}>Save</button><button class="btn sm" onclick={() => (editing = false)}>Cancel</button>
    {:else}
      <h1>{display(c)}</h1>
      <button class="btn sm" onclick={() => { label = c.label || c.hostname; editing = true }}>Rename</button>
    {/if}
    <span class="badge {c.online ? 'ok' : ''}">{c.online ? 'online' : 'offline'}</span>
    {#if c.wifi}<span class="badge acc">Wi-Fi {c.ssid}{c.band ? ' · ' + c.band : ''}</span>{:else}<span class="badge">wired</span>{/if}
    {#if c.vendor}<span class="badge">{c.vendor}</span>{/if}
  </div>

  <div class="grid g4" style="margin:16px 0">
    <div class="card card-b kpi"><div class="l">Address</div><div class="v mono" style="font-size:17px">{c.ip || '—'}</div><div class="l mono">{c.mac}</div></div>
    <div class="card card-b kpi"><div class="l">Connected to</div><div class="v" style="font-size:17px">{c.device || '—'}</div><div class="l">{c.wifi ? (c.signal ? sq.txt + ' · ' : '') + (c.tx_rate || '') : c.iface}</div></div>
    <div class="card card-b kpi"><div class="l">Right now</div><div class="v num" style="font-size:17px">↓ {bps(liveRate?.down_bps)} <span class="muted">↑ {bps(liveRate?.up_bps)}</span></div><div class="l">30 s average</div></div>
    <div class="card card-b kpi"><div class="l">Seen</div><div class="v" style="font-size:17px">{datetime(c.last_seen)}</div><div class="l">first {datetime(c.first_seen)}</div></div>
  </div>

  {#if x.new_destinations?.length}
    <div class="warnbox" style="margin-bottom:16px"><b>{x.new_destinations.length} destination{x.new_destinations.length > 1 ? 's' : ''} contacted for the first time in the last 24 h:</b>
      <span class="mono"> {x.new_destinations.slice(0, 8).join(', ')}{x.new_destinations.length > 8 ? ' …' : ''}</span></div>
  {/if}

  <div class="card"><div class="card-h"><h2>Traffic</h2><div class="legend"><span><i style="background:var(--down)"></i>Download</span><span><i style="background:var(--up)"></i>Upload</span></div></div>
    <div class="card-b"><Chart series={chart} fmt={bps} height={180} /></div></div>

  <div class="grid g2" style="margin-top:16px">
    <div class="card"><div class="card-h"><h2>Top destinations</h2></div><TopList rows={x.top_hosts} /></div>
    <div class="card"><div class="card-h"><h2>Top services</h2><span class="muted" style="font-size:12.5px">Klick: wer nutzt das noch?</span></div><TopList rows={x.top_services} onpick={r => go('services/' + encodeURIComponent(r.key))} /></div>
    <div class="card"><div class="card-h"><h2>Top ports</h2></div><TopList rows={x.top_ports} /></div>
    <div class="card"><div class="card-h"><h2>Countries</h2></div><TopList rows={x.top_countries} mode="country" empty="No GeoIP database configured" /></div>
  </div>

  <div class="card" style="margin-top:16px">
    <div class="card-h"><h2>Connections</h2>
      <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center">
        <input class="input" placeholder="Filter IP, host, port, service…" bind:value={filter} aria-label="Filter" />
        <div class="tabs">{#each [['all', 'All'], ['u', '↑ Out'], ['d', '↓ In'], ['i', '⇄ LAN']] as [id, l]}<button class:on={dirF === id} onclick={() => (dirF = id)}>{l}</button>{/each}</div>
        <button class="btn" onclick={() => (tail = !tail)}>{tail ? 'Pause live tail' : 'Resume live tail'}</button>
        <button class="btn" onclick={() => download('/export/connections/' + encodeURIComponent(mac) + '?range=' + rg())}>CSV</button>
      </div></div>
    <div class="scroll" style="max-height:520px;overflow-y:auto">
      <table>
        <thead><tr><th>Time</th><th>Dir</th><th>Destination</th><th>Ports (src → dst)</th><th>Service</th><th>Firewall</th><th>Via</th><th>Country / AS</th><th class="r">Bytes</th><th></th></tr></thead>
        <tbody>
          {#each shown.slice(0, 400) as r (r.ts + r.rip + r.rport + r.cport + r.bytes + r.dir)}
            <tr><td class="mono muted">{clock(r.ts)}</td><td class:dn={r.dir === 'd'} class:upc={r.dir === 'u'}>{arrow[r.dir]}</td>
              <td><span class="mono">{r.rip}</span>{#if r.host}<div class="muted">{r.host}</div>{/if}</td>
              <td class="mono">{protoName(r.proto)} {r.dir === 'd' ? r.rport + ' → ' + r.cport : r.cport + ' → ' + r.rport}</td>
              <td>{#if r.svc}<a class="badge" href="#/services/{encodeURIComponent(r.svc)}">{r.svc}</a>{/if}</td>
              <td>{#if r.verdict}<span class="badge {vc[r.verdict]}" title={r.rule}>{vt[r.verdict]}</span>{:else}<span class="muted" title="Kein Firewall-Logging für diesen Router aktiv">–</span>{/if}</td>
              <td class="muted">{r.device || ''}</td>
              <td>{flag(r.cc)} {r.cc} <span class="muted">{r.asorg}</span></td><td class="r num">{bytes(r.bytes)}</td>
              <td class="r"><button class="btn sm" title="Als Service klassifizieren" onclick={() => (dlg = { kind: r.host ? 'host' : 'ip', value: r.host ? r.host.split('.').slice(-2).join('.') : r.rip, name: r.svc || '', hint: `Verbindung zu ${r.host || r.rip}:${r.rport}` })}>＋</button></td></tr>
          {/each}
        </tbody></table>
      {#if !shown.length}<div class="empty">{cerr || 'No connections recorded in this range'}</div>{/if}
    </div>
  </div>

  {#if blocked.data?.length}
    <div class="card" style="margin-top:16px"><div class="card-h"><h2>Blockierte Verbindungsversuche</h2><span class="muted" style="font-size:12.5px">von der Firewall verworfen – tauchen in den Flows nicht auf</span></div>
      <div class="scroll" style="max-height:300px;overflow-y:auto"><table><thead><tr><th>Zeit</th><th>Ziel</th><th>Proto</th><th>Regel</th><th>Router</th></tr></thead>
        <tbody>{#each blocked.data as e, i (e.ts + e.dst + e.dport + i)}<tr><td class="mono muted">{clock(e.ts)}</td><td class="mono">{e.dst}:{e.dport}</td><td class="mono">{e.proto}</td><td>{e.rule}</td><td class="muted">{e.device}</td></tr>{/each}</tbody></table></div></div>
  {/if}

  <div class="grid g2" style="margin-top:16px">
    <div class="card"><div class="card-h"><h2>IP history</h2></div><table><tbody>
      {#each x.ip_history as h}<tr><td class="mono">{h.ip}</td><td class="r muted">{datetime(h.first)} → {datetime(h.last)}</td></tr>{/each}
    </tbody></table>{#if !x.ip_history.length}<div class="empty">—</div>{/if}</div>
    <div class="card"><div class="card-h"><h2>Roaming</h2></div><table><tbody>
      {#each x.roams as r}<tr><td>{r.from} → <b>{r.to}</b></td><td class="r muted">{datetime(r.ts)}</td></tr>{/each}
    </tbody></table>{#if !x.roams.length}<div class="empty">No roaming events</div>{/if}</div>
  </div>
{/if}
{#if dlg}<ClassifyDialog init={dlg} onclose={() => (dlg = null)} />{/if}

<style>
  .head { margin-bottom: 14px; } .title { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
  .dn { color: var(--down); font-weight: 600; white-space: nowrap; } .upc { color: var(--up); font-weight: 600; white-space: nowrap; }
</style>
