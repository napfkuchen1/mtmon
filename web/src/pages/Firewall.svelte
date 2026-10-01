<script>
  import { api } from '../lib/api.js'
  import { app, go } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { num, clock, datetime } from '../lib/format.js'
  import Chart from '../lib/Chart.svelte'
  const rg = () => (app.range === 'live' ? '1h' : app.range)
  const s = poll(() => api('/firewall/summary?range=' + rg()), 6000)
  let verdict = $state(''), q = $state('')
  const ev = poll(() => api(`/firewall/events?range=${rg()}&limit=300${verdict ? '&verdict=' + verdict : ''}`), 4000)
  const x = $derived(s.data)
  const sm = $derived(x?.summary)
  const rows = $derived((ev.data || []).filter(e => !q || (e.src + e.dst + e.rule + e.dport + e.device + e.proto).toLowerCase().includes(q.toLowerCase())))
  const chart = $derived([
    { name: 'Blocked', color: 'var(--bad)', points: (sm?.series || []).map(p => ({ x: p.ts, y: p.blocked })) },
    { name: 'Allowed (logged)', color: 'var(--ok)', points: (sm?.series || []).map(p => ({ x: p.ts, y: p.allowed })) }
  ])
  const vcls = { blocked: 'bad', allowed: 'ok', logged: '' }
  const vtxt = { blocked: 'blockiert', allowed: 'durchgelassen', logged: 'geloggt' }
</script>

<div class="head"><h1>Firewall</h1><span class="muted">Events aus RouterOS-Syslog · welche Regel hat getroffen</span></div>
{#if s.error}<div class="warnbox">{s.error}</div>{/if}

{#if x && !x.enabled}
  <div class="card empty" style="padding:40px 16px">
    <h2 style="font-size:17px;margin-bottom:6px">Firewall-Logging ist noch nicht aktiv</h2>
    <p class="muted" style="max-width:560px;margin:0 auto 14px">Beim Hinzufügen eines Routers kann mtmon Syslog und das Logging der Drop-Regeln einrichten. Danach siehst du hier, was die Firewall blockt und bei jeder Verbindung, welche Regel gegriffen hat.</p>
    <button class="btn primary" onclick={() => go('devices')}>Zu den Geräten</button>
  </div>
{:else if x}
  <div class="grid g4" style="margin-bottom:16px">
    <div class="card card-b kpi"><div class="l">Blockiert</div><div class="v num" style="color:var(--bad)">{num(sm.blocked)}</div><div class="l">Pakete/Verbindungen in {rg()}</div></div>
    <div class="card card-b kpi"><div class="l">Durchgelassen (geloggt)</div><div class="v num" style="color:var(--ok)">{num(sm.allowed)}</div><div class="l">Regeln mit accept/passthrough-Log</div></div>
    <div class="card card-b kpi"><div class="l">Syslog empfangen</div><div class="v num">{num(x.syslog.parsed)}</div><div class="l">{num(x.syslog.unparsed)} nicht lesbar · {num(x.syslog.dropped_unknown_source)} unbekannter Absender</div></div>
    <div class="card card-b kpi"><div class="l">Geloggte Regeln</div><div class="v num">{x.rules.length}</div><div class="l">über {new Set(x.rules.map(r => r.device)).size} Gerät(e)</div></div>
  </div>

  <div class="card"><div class="card-h"><h2>Verlauf</h2><div class="legend"><span><i style="background:var(--bad)"></i>Blockiert</span><span><i style="background:var(--ok)"></i>Durchgelassen</span></div></div>
    <div class="card-b"><Chart series={chart} bars step={3600} fmt={v => num(Math.round(v))} height={150} empty="Noch keine Events in diesem Zeitraum" /></div></div>

  <div class="grid g3" style="margin-top:16px">
    <div class="card"><div class="card-h"><h2>Regeln nach Treffern</h2></div>
      <table><tbody>{#each sm.rules as r}<tr><td><span class="badge {vcls[r.verdict]}">{vtxt[r.verdict]}</span></td><td>{r.rule}<div class="muted" style="font-size:12px">{r.device}</div></td><td class="r num"><b>{num(r.hits)}</b></td></tr>{/each}</tbody></table>
      {#if !sm.rules.length}<div class="empty">Keine Treffer</div>{/if}</div>
    <div class="card"><div class="card-h"><h2>Top blockierte Quellen</h2></div>
      <table><tbody>{#each sm.top_blocked_src as t}<tr><td class="mono">{t.key}</td><td class="r num"><b>{num(t.hits)}</b></td></tr>{/each}</tbody></table>
      {#if !sm.top_blocked_src.length}<div class="empty">—</div>{/if}</div>
    <div class="card"><div class="card-h"><h2>Top blockierte Ziele</h2></div>
      <table><tbody>{#each sm.top_blocked_dst as t}<tr><td class="mono">{t.key}{t.port ? ':' + t.port : ''}</td><td class="r num"><b>{num(t.hits)}</b></td></tr>{/each}</tbody></table>
      {#if !sm.top_blocked_dst.length}<div class="empty">—</div>{/if}</div>
  </div>

  <div class="card" style="margin-top:16px">
    <div class="card-h"><h2>Letzte Events</h2>
      <div style="display:flex;gap:8px;flex-wrap:wrap">
        <input class="input" placeholder="Filter IP, Port, Regel …" bind:value={q} aria-label="Filter" />
        <div class="tabs">{#each [['', 'Alle'], ['blocked', 'Blockiert'], ['allowed', 'Durchgelassen']] as [id, l]}<button class:on={verdict === id} onclick={() => (verdict = id)}>{l}</button>{/each}</div>
      </div></div>
    <div class="scroll" style="max-height:560px;overflow-y:auto"><table>
      <thead><tr><th>Zeit</th><th>Gerät</th><th>Ergebnis</th><th>Regel</th><th>Quelle</th><th>Ziel</th><th>Proto</th><th>Interface</th></tr></thead>
      <tbody>{#each rows as e, i (e.ts + e.src + e.sport + e.dst + e.dport + e.prefix + i)}
        <tr><td class="mono muted" title={datetime(e.ts)}>{clock(e.ts)}</td><td>{e.device}</td>
          <td><span class="badge {vcls[e.verdict]}">{vtxt[e.verdict]}</span></td>
          <td>{e.rule}</td>
          <td class="mono">{e.src}{e.sport ? ':' + e.sport : ''}</td>
          <td class="mono">{e.dst}{e.dport ? ':' + e.dport : ''}{#if e.nat}<div class="muted" style="font-size:11.5px">NAT {e.nat}</div>{/if}</td>
          <td class="mono">{e.proto}{e.flags ? ' (' + e.flags + ')' : ''}</td>
          <td class="muted">{e.in_if} → {e.out_if}</td></tr>
      {/each}</tbody></table>
      {#if !rows.length}<div class="empty">Keine Events in diesem Zeitraum</div>{/if}</div>
  </div>
{/if}
<style>.head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 18px; }</style>
