<script>
  import { api } from '../lib/api.js'
  import { app, go } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { bytes, bps, num } from '../lib/format.js'
  import { t, tr, i18n } from '../lib/i18n.svelte.js'
  import Chart from '../lib/Chart.svelte'
  import TopList from '../lib/TopList.svelte'
  import Spark from '../lib/Spark.svelte'
  import WorldMap from '../lib/WorldMap.svelte'

  const bucket = { live: 60, '1h': 60, '24h': 900, '7d': 3600, '30d': 14400 }
  const d = poll(() => api('/overview?range=' + app.range), 5000)
  const o = $derived(d.data)

  const series = $derived.by(() => {
    if (app.range === 'live') {
      return [
        { name: t('Download'), color: 'var(--down)', points: app.history.map(h => ({ x: h.t, y: h.down })) },
        { name: t('Upload'), color: 'var(--up)', points: app.history.map(h => ({ x: h.t, y: h.up })) }
      ]
    }
    const b = bucket[app.range] || 60
    const pts = o?.series || []
    return [
      { name: t('Download'), color: 'var(--down)', points: pts.map(p => ({ x: p.ts, y: (p.down * 8) / b })) },
      { name: t('Upload'), color: 'var(--up)', points: pts.map(p => ({ x: p.ts, y: (p.up * 8) / b })) }
    ]
  })
  const xfmt = x => app.range === '7d' || app.range === '30d' ? new Date(x * 1000).toLocaleDateString(i18n.locale, { month: 'short', day: 'numeric' }) : new Date(x * 1000).toLocaleTimeString(i18n.locale, { hour12: false, hour: '2-digit', minute: '2-digit' })
  // change vs. the equally long period before ("+12 %"); hidden when there is no comparable data
  const delta = (cur, prev) => {
    if (!o?.prev || !prev) return null
    const p = Math.round(((cur - prev) / prev) * 100)
    return { p, txt: Math.abs(p) > 999 ? (p > 0 ? '>+999 %' : '<−999 %') : (p > 0 ? '+' : '') + p + ' %', cls: Math.abs(p) < 10 ? 'flat' : p > 0 ? 'up' : 'down' }
  }
  const dDown = $derived(o ? delta(o.bytes_down, o.prev?.bytes_down) : null)
  const dUp = $derived(o ? delta(o.bytes_up, o.prev?.bytes_up) : null)
  const dFlows = $derived(o ? delta(o.flows, o.prev?.flows) : null)
  const sparkDown = $derived(app.range === 'live' ? app.history.map(h => h.down) : (o?.series || []).map(p => p.down))
  const sparkUp = $derived(app.range === 'live' ? app.history.map(h => h.up) : (o?.series || []).map(p => p.up))
  const pickClient = r => go(r.device ? 'device/' + encodeURIComponent(r.device) : 'client/' + encodeURIComponent(r.key))
</script>

<div class="head"><h1>{t('Overview')}</h1><span class="muted">{app.range === 'live' ? t('Last 5 minutes') : t('Last {range}', { range: app.range })}</span></div>

{#if d.error}<div class="warnbox">{t('Could not load data: {err}', { err: tr(d.error) })}</div>{/if}

<div class="grid g6 kpis">
  <div class="card card-b kpi"><div class="l">{t('Clients online')}</div><div class="v num">{o ? num(o.clients_online) : '—'}</div><div class="l">{t('of {n} known', { n: o ? num(o.clients_total) : '—' })}</div></div>
  <div class="card card-b kpi"><div class="l">{t('Devices up')}</div><div class="v num">{o ? o.devices_up : '—'}<span class="muted" style="font-size:16px"> / {o ? o.devices_total : '—'}</span></div><div class="l">{o && o.devices_up < o.devices_total ? t('check Devices') : t('all reachable')}</div></div>
  <div class="card card-b kpi"><div class="l">{t('Download')}</div><div class="v num" style="color:var(--down)">{o ? bytes(o.bytes_down) : '—'}</div><div class="l">{t('now {rate}', { rate: bps(app.live.down_bps) })}{#if dDown} <span class="dl {dDown.cls}" title={t('vs. the previous period')}>{dDown.txt}</span>{/if}</div><Spark points={sparkDown} color="var(--down)" /></div>
  <div class="card card-b kpi"><div class="l">{t('Upload')}</div><div class="v num" style="color:var(--up)">{o ? bytes(o.bytes_up) : '—'}</div><div class="l">{t('now {rate}', { rate: bps(app.live.up_bps) })}{#if dUp} <span class="dl {dUp.cls}" title={t('vs. the previous period')}>{dUp.txt}</span>{/if}</div><Spark points={sparkUp} color="var(--up)" /></div>
  <div class="card card-b kpi"><div class="l">{t('Flows')}</div><div class="v num">{o ? num(o.flows) : '—'}</div><div class="l">{app.live.flows_ps ? app.live.flows_ps.toFixed(0) + ' / s' : t('idle')}{#if dFlows} <span class="dl {dFlows.cls}" title={t('vs. the previous period')}>{dFlows.txt}</span>{/if}</div></div>
  <a class="card card-b kpi" href="#/alerts" style="color:inherit;text-decoration:none"><div class="l">{t('Open alerts')}</div><div class="v num" style="color:{o?.alerts_open ? 'var(--bad)' : 'inherit'}">{o ? o.alerts_open : '—'}</div><div class="l">{t('view alerts →')}</div></a>
</div>

<div class="card" style="margin-top:16px">
  <div class="card-h"><h2>{t('Traffic')}</h2>
    <div class="legend"><span><i style="background:var(--down)"></i>{t('Download')}</span><span><i style="background:var(--up)"></i>{t('Upload')}</span></div>
  </div>
  <div class="card-b"><Chart {series} fmt={bps} {xfmt} height={220} empty={app.range === 'live' ? t('Waiting for live data…') : t('No traffic recorded in this range yet')} /></div>
</div>

<div class="grid g2" style="margin-top:16px">
  <div class="card"><div class="card-h"><h2>{t('Top clients')}</h2><a href="#/insights">{t('All →')}</a></div><TopList rows={o?.top_clients || []} onpick={pickClient} /></div>
  <div class="card"><div class="card-h"><h2>{t('Top destinations')}</h2><a href="#/insights/hosts">{t('All →')}</a></div><TopList rows={o?.top_hosts || []} /></div>
  <div class="card"><div class="card-h"><h2>{t('Top services')}</h2><a href="#/insights/services">{t('All →')}</a></div><TopList rows={o?.top_services || []} /></div>
  <div class="card"><div class="card-h"><h2>{t('Top countries')}</h2><a href="#/insights/country">{t('All →')}</a></div>{#if o?.top_countries?.length}<WorldMap rows={o.top_countries} />{/if}<TopList rows={o?.top_countries || []} mode="country" empty={t('No GeoIP database configured (see Settings)')} emptyIcon="generic" emptyHref="#/settings" emptyAction={t('Set up in Settings')} /></div>
</div>

<style>
  .kpi .l { display: flex; flex-wrap: wrap; gap: 2px 8px; align-items: baseline; }
  .dl { font-weight: 600; font-size: 11.5px; padding: 0 6px; border-radius: 999px; background: var(--card-2); border: 1px solid var(--border); white-space: nowrap; }
  .dl.up { color: var(--warn); } .dl.down { color: var(--ok); } .dl.flat { color: var(--muted); }
  .head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 18px; }
</style>
