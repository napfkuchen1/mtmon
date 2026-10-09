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
  let verdict = $state(''), q = $state(''), showOwn = $state(false)
  const ev = poll(() => api(`/firewall/events?range=${rg()}&limit=300${verdict ? '&verdict=' + verdict : ''}${showOwn ? '&own=1' : ''}`), 4000)
  const ownCmd = '/ip firewall filter set [find where comment~"mtmon" && log=yes] log=no'
  const sample = $derived(x?.unparsed_sample || [])
  const why = r => (r === 'format' ? t('Firewall line in a layout mtmon does not know') : t('Not a firewall line (other log topic)'))
  async function copy(v) { try { await navigator.clipboard.writeText(v) } catch {} }
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

{#if !x && !s.error}
  <div class="grid g4" style="margin-bottom:16px" aria-busy="true" aria-label={t('Loading…')}>
    {#each [0, 1, 2, 3] as _}<div class="card card-b kpi sk"><div class="l">&nbsp;</div><div class="v">&nbsp;</div><div class="l">&nbsp;</div></div>{/each}
  </div>
  <div class="card sk" style="height:210px"></div>
{:else if x && !x.enabled}
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

  {#if sample.length}
    <details class="card card-b unp">
      <summary>{t('Why are {n} lines unreadable?', { n: num(x.syslog.unparsed) })}</summary>
      <p class="muted">{t('Router log lines that are not firewall events (for example system or login messages sent to the same syslog target) are counted as unreadable. They are harmless. Latest examples:')}</p>
      <ul>{#each sample as l}<li><span class="badge">{l.device}</span> <span class="muted">{why(l.reason)}</span><div class="mono ln">{l.line}</div></li>{/each}</ul>
    </details>
  {/if}
  {#if sm.own_hits}
    <div class="card card-b own">
      <b>{t("mtmon's own access is logged ({n} hits, not counted above)", { n: num(sm.own_hits) })}</b>
      <p class="muted">{t("A firewall rule that accepts mtmon's REST access has logging on, so every poll shows up here. You can switch the logging off; the access itself keeps working.")}</p>
      <div class="cmdh"><span class="muted">{t('RouterOS terminal')}</span><button class="btn sm" onclick={() => copy(ownCmd)}>{t('Copy')}</button></div>
      <pre class="code">{ownCmd}</pre>
    </div>
  {/if}

  <div class="card"><div class="card-h"><h2>{t('History')}</h2><div class="legend"><span><i style="background:var(--bad)"></i>{t('Blocked')}</span><span><i style="background:var(--ok)"></i>{t('Allowed')}</span></div></div>
    <div class="card-b"><Chart series={chart} bars step={3600} fmt={v => num(Math.round(v))} height={150} empty={t('No events in this range yet')} /></div></div>

  <div class="grid g3" style="margin-top:16px">
    <div class="card"><div class="card-h"><h2>{t('Rules by hits')}</h2></div>
      <table><tbody>{#each sm.rules.filter(r => showOwn || !r.own) as r}<tr><td><span class="badge {vcls[r.verdict]}">{vtxt[r.verdict]}</span></td><td>{tr(r.rule)}<div class="muted" style="font-size:12px">{r.device}</div></td><td class="r num"><b>{num(r.hits)}</b></td></tr>{/each}</tbody></table>
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
        <label class="chk"><input type="checkbox" bind:checked={showOwn} /> {t("Show mtmon's own traffic")}</label>
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
<style>
  .head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 18px; }
  .sk { background: linear-gradient(90deg, var(--card-2), var(--card), var(--card-2)); background-size: 200% 100%; animation: sk 1.4s linear infinite; }
  @keyframes sk { to { background-position: -200% 0; } }
  @media (prefers-reduced-motion: reduce) { .sk { animation: none; } }
  .unp, .own { margin-bottom: 16px; }
  .unp summary { cursor: pointer; font-weight: 600; }
  .unp ul { list-style: none; padding: 0; margin: 8px 0 0; }
  .unp li { margin: 8px 0; }
  .ln { margin-top: 3px; padding: 6px 10px; background: var(--card-2); border: 1px solid var(--border); border-radius: 6px; font-size: 12px; overflow-wrap: anywhere; }
  .own p { margin: 4px 0 10px; }
  .cmdh { display: flex; justify-content: space-between; align-items: center; margin: 8px 0 4px; }
  .chk { display: inline-flex; align-items: center; gap: 6px; }
</style>
