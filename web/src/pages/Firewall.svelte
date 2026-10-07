<script>
  import { api } from '../lib/api.js'
  import { app, go } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { num, clock, datetime } from '../lib/format.js'
  import { t, tr } from '../lib/i18n.svelte.js'
  import Chart from '../lib/Chart.svelte'
  import IPName from '../lib/IPName.svelte'
  const rg = () => (app.range === 'live' ? '1h' : app.range)
  const s = poll(() => api('/firewall/summary?range=' + rg()), 6000)
  let verdict = $state(''), q = $state('')
  const ev = poll(() => api(`/firewall/events?range=${rg()}&limit=300${verdict ? '&verdict=' + verdict : ''}`), 4000)
  const x = $derived(s.data)
  const sm = $derived(x?.summary)
  const rows = $derived((ev.data || []).filter(e => !q || (e.src + e.dst + e.rule + e.dport + e.device + e.proto + (e.src_name || '') + (e.dst_name || '') + (e.src_org || '') + (e.dst_org || '')).toLowerCase().includes(q.toLowerCase())))
  const chart = $derived([
    { name: t('Blocked'), color: 'var(--bad)', points: (sm?.series || []).map(p => ({ x: p.ts, y: p.blocked })) },
    { name: t('Allowed (logged)'), color: 'var(--ok)', points: (sm?.series || []).map(p => ({ x: p.ts, y: p.allowed })) }
  ])
  const vcls = { blocked: 'bad', allowed: 'ok', logged: '' }
  const vtxt = $derived({ blocked: t('blocked'), allowed: t('allowed'), logged: t('logged') })
</script>

<div class="head"><h1>{t('Firewall')}</h1><span class="muted">{t('Events from RouterOS syslog · which rule matched')}</span></div>
{#if s.error}<div class="warnbox">{tr(s.error)}</div>{/if}

{#if x && !x.enabled}
  <div class="card empty" style="padding:40px 16px">
    <h2 style="font-size:17px;margin-bottom:6px">{t('Firewall logging is not active yet')}</h2>
    <p class="muted" style="max-width:560px;margin:0 auto 14px">{t('When adding a router, mtmon can set up syslog and logging of the drop rules. After that you will see here what the firewall blocks and which rule matched for each connection.')}</p>
    <button class="btn primary" onclick={() => go('devices')}>{t('Go to devices')}</button>
  </div>
{:else if x}
  <div class="grid g4" style="margin-bottom:16px">
    <div class="card card-b kpi"><div class="l">{t('Blocked')}</div><div class="v num" style="color:var(--bad)">{num(sm.blocked)}</div><div class="l">{t('Packets/connections in {range}', { range: rg() })}</div></div>
    <div class="card card-b kpi"><div class="l">{t('Allowed (logged)')}</div><div class="v num" style="color:var(--ok)">{num(sm.allowed)}</div><div class="l">{t('Rules with accept/passthrough log')}</div></div>
    <div class="card card-b kpi"><div class="l">{t('Syslog received')}</div><div class="v num">{num(x.syslog.parsed)}</div><div class="l">{t('{a} unreadable · {b} unknown sender', { a: num(x.syslog.unparsed), b: num(x.syslog.dropped_unknown_source) })}</div></div>
    <div class="card card-b kpi"><div class="l">{t('Logged rules')}</div><div class="v num">{x.rules.length}</div><div class="l">{t('across {n} device(s)', { n: new Set(x.rules.map(r => r.device)).size })}</div></div>
  </div>

  <div class="card"><div class="card-h"><h2>{t('History')}</h2><div class="legend"><span><i style="background:var(--bad)"></i>{t('Blocked')}</span><span><i style="background:var(--ok)"></i>{t('Allowed')}</span></div></div>
    <div class="card-b"><Chart series={chart} bars step={3600} fmt={v => num(Math.round(v))} height={150} empty={t('No events in this range yet')} /></div></div>

  <div class="grid g3" style="margin-top:16px">
    <div class="card"><div class="card-h"><h2>{t('Rules by hits')}</h2></div>
      <table><tbody>{#each sm.rules as r}<tr><td><span class="badge {vcls[r.verdict]}">{vtxt[r.verdict]}</span></td><td>{tr(r.rule)}<div class="muted" style="font-size:12px">{r.device}</div></td><td class="r num"><b>{num(r.hits)}</b></td></tr>{/each}</tbody></table>
      {#if !sm.rules.length}<div class="empty">{t('No hits')}</div>{/if}</div>
    <div class="card"><div class="card-h"><h2>{t('Top blocked sources')}</h2></div>
      <table><tbody>{#each sm.top_blocked_src as x}<tr><td><IPName ip={x.key} name={x.name} org={x.org} cc={x.country} /></td><td class="r num"><b>{num(x.hits)}</b></td></tr>{/each}</tbody></table>
      {#if !sm.top_blocked_src.length}<div class="empty">—</div>{/if}</div>
    <div class="card"><div class="card-h"><h2>{t('Top blocked destinations')}</h2></div>
      <table><tbody>{#each sm.top_blocked_dst as x}<tr><td><IPName ip={x.key} port={x.port} name={x.name} org={x.org} cc={x.country} /></td><td class="r num"><b>{num(x.hits)}</b></td></tr>{/each}</tbody></table>
      {#if !sm.top_blocked_dst.length}<div class="empty">—</div>{/if}</div>
  </div>

  <div class="card" style="margin-top:16px">
    <div class="card-h"><h2>{t('Latest events')}</h2>
      <div style="display:flex;gap:8px;flex-wrap:wrap">
        <input class="input" placeholder={t('Filter IP, port, rule …')} bind:value={q} aria-label={t('Filter')} />
        <div class="tabs">{#each [['', t('All')], ['blocked', t('Blocked')], ['allowed', t('Allowed')]] as [id, l]}<button class:on={verdict === id} onclick={() => (verdict = id)}>{l}</button>{/each}</div>
      </div></div>
    <div class="scroll" style="max-height:560px;overflow-y:auto"><table>
      <thead><tr><th>{t('Time')}</th><th>{t('Device')}</th><th>{t('Result')}</th><th>{t('Rule')}</th><th>{t('Source')}</th><th>{t('Target')}</th><th>{t('Proto')}</th><th>{t('Interface')}</th></tr></thead>
      <tbody>{#each rows as e, i (e.ts + e.src + e.sport + e.dst + e.dport + e.prefix + i)}
        <tr><td class="mono muted" title={datetime(e.ts)}>{clock(e.ts)}</td><td>{e.device}</td>
          <td><span class="badge {vcls[e.verdict]}">{vtxt[e.verdict]}</span></td>
          <td>{tr(e.rule)}</td>
          <td><IPName ip={e.src} port={e.sport} name={e.src_name} org={e.src_org} cc={e.src_country} /></td>
          <td><IPName ip={e.dst} port={e.dport} name={e.dst_name} org={e.dst_org} cc={e.dst_country} />{#if e.nat}<div class="muted" style="font-size:11.5px">NAT {e.nat}</div>{/if}</td>
          <td class="mono">{e.proto}{e.flags ? ' (' + e.flags + ')' : ''}</td>
          <td class="muted">{e.in_if} → {e.out_if}</td></tr>
      {/each}</tbody></table>
      {#if !rows.length}<div class="empty">{t('No events in this range')}</div>{/if}</div>
  </div>
{/if}
<style>.head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 18px; }</style>
