<script>
  import { api } from '../lib/api.js'
  import { app, go } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { bps, dur, display } from '../lib/format.js'
  import Chart from '../lib/Chart.svelte'
  import Modal from '../lib/Modal.svelte'
  import { toast } from '../lib/state.svelte.js'
  import { datetime } from '../lib/format.js'
  const name = $derived(app.route.params.id)
  const d = poll(() => api('/devices/' + encodeURIComponent(app.route.params.id) + '?range=' + (app.range === 'live' ? '1h' : app.range)), 5000)
  const x = $derived(d.data)
  const ifaces = $derived((x?.ifaces || []).slice().sort((a, b) => (b.rx_bps + b.tx_bps) - (a.rx_bps + a.tx_bps)))
  const cpu = $derived([{ name: 'CPU %', color: 'var(--accent)', points: (x?.metrics || []).map(m => ({ x: m.ts, y: m.cpu })) }])
  const mem = $derived([{ name: 'RAM %', color: 'var(--down)', points: (x?.metrics || []).map(m => ({ x: m.ts, y: m.mem_total ? (m.mem_used * 100) / m.mem_total : 0 })) }])
  let showAll = $state(false)
  const caps = $derived(x?.caps && x.caps.version ? x.caps : null)
  const man = poll(() => api('/devices/' + encodeURIComponent(app.route.params.id) + '/manifest'), 0)
  let off = $state(false), oUser = $state('admin'), oPass = $state(''), oPurge = $state(false), oBusy = $state(false), oRes = $state(null), oErr = $state('')
  async function offboard(mode) {
    oBusy = true; oErr = ''; oRes = null
    try {
      oRes = await api('/devices/' + encodeURIComponent(name) + '/offboard', { method: 'POST', body: { user: oUser, pass: oPass, mode, purge: oPurge } })
      oPass = ''
      if (oRes.ok) { toast('Gerät entfernt'); setTimeout(() => go('devices'), 1200) }
    } catch (e) { oErr = e.message } finally { oBusy = false }
  }
  const sCls = { applied: 'ok', reverted: '', 'revert-failed': 'bad' }
</script>

<div class="head"><a href="#/devices">← Devices</a><h1>{name}</h1>
  {#if x}<span class="badge">{x.role}</span><span class="mono muted">{x.addr}</span>{/if}</div>
{#if d.error}<div class="warnbox">{d.error}</div>{/if}

{#if x}
  <div class="grid g4" style="margin-bottom:16px">
    <div class="card card-b kpi"><div class="l">Model</div><div class="v" style="font-size:18px">{x.state?.model || '—'}</div><div class="l">{x.state?.identity}</div></div>
    <div class="card card-b kpi"><div class="l">RouterOS</div><div class="v" style="font-size:18px">{x.state?.version || '—'}</div><div class="l">{x.state?.info?.upgrade && x.state.info.upgrade !== x.state.info.firmware ? 'firmware upgrade available' : 'firmware current'}</div></div>
    <div class="card card-b kpi"><div class="l">Clients</div><div class="v num">{x.clients.length}</div><div class="l">online via this device</div></div>
    <div class="card card-b kpi"><div class="l">Status</div><div class="v" style="font-size:18px"><span class="dot" class:ok={app.live.devices.find(v => v.name === name)?.up} class:bad={!app.live.devices.find(v => v.name === name)?.up}></span>
      {app.live.devices.find(v => v.name === name)?.up ? ' Online' : ' Offline'}</div><div class="l">{x.state?.last_err || ''}</div></div>
  </div>

  {#if caps}
    <div class="card" style="margin-bottom:16px"><div class="card-h"><h2>Capabilities</h2><span class="muted" style="font-size:12.5px">automatisch erkannt beim Hinzufügen</span></div>
      <div class="card-b caps">
        <div><span class="l">Rolle</span><b>{caps.role}</b></div>
        <div><span class="l">WLAN</span><b>{caps.wifi_stack === 'wifi' ? caps.wifi_ifs.map(w => w.name + (w.ssid ? ' “' + w.ssid + '”' : '') + (w.band ? ' ' + w.band : '')).join(', ') : caps.wifi_stack === 'wireless' ? 'Legacy' : '—'}</b></div>
        <div><span class="l">Netz</span><b>{caps.dhcp_server ? 'DHCP' : '—'} · {caps.nat ? 'NAT' : 'kein NAT'} · {caps.bridges?.length || 0} Bridge(s){caps.hw_offload ? ' · HW-Offload' : ''}</b></div>
        <div><span class="l">Firewall</span><b>{caps.filter_rules?.length || 0} Regeln{caps.fasttrack ? ' · FastTrack' : ''}</b></div>
        <div><span class="l">Hardware</span><b>{[caps.arch, caps.cpu_count ? caps.cpu_count + ' Kerne' : '', caps.mem_total ? Math.round(caps.mem_total / 1048576) + ' MB' : ''].filter(Boolean).join(' · ') || '—'}</b></div>
        <div><span class="l">Pakete</span><b>{(caps.packages || []).join(', ')}</b></div>
      </div></div>
  {/if}

  <div class="grid g2">
    <div class="card"><div class="card-h"><h2>CPU</h2></div><div class="card-b"><Chart series={cpu} fmt={v => v.toFixed(0) + '%'} height={150} empty="Collecting metrics (1 sample/min)…" /></div></div>
    <div class="card"><div class="card-h"><h2>Memory</h2></div><div class="card-b"><Chart series={mem} fmt={v => v.toFixed(0) + '%'} height={150} empty="Collecting metrics (1 sample/min)…" /></div></div>
  </div>

  <div class="card" style="margin-top:16px">
    <div class="card-h"><h2>Interfaces</h2><button class="btn sm" onclick={() => (showAll = !showAll)}>{showAll ? 'Hide idle' : 'Show all'}</button></div>
    <div class="scroll"><table>
      <thead><tr><th>Name</th><th>Type</th><th>Link</th><th class="r">↓ RX</th><th class="r">↑ TX</th></tr></thead>
      <tbody>
        {#each ifaces.filter(i => showAll || i.rx_bps + i.tx_bps > 0) as i (i.iface)}
          <tr><td><b>{i.iface}</b></td><td class="muted">{i.type}</td><td><span class="dot" class:ok={i.running} class:bad={!i.running}></span> {i.running ? 'up' : 'down'}</td>
            <td class="r num">{bps(i.rx_bps)}</td><td class="r num">{bps(i.tx_bps)}</td></tr>
        {/each}
      </tbody></table>
      {#if !ifaces.length}<div class="empty">No interface data yet</div>{/if}
    </div>
  </div>

  <div class="grid g2" style="margin-top:16px">
    <div class="card"><div class="card-h"><h2>Connected clients ({x.clients.length})</h2></div>
      <div class="scroll"><table><tbody>
        {#each x.clients as c (c.mac)}
          <tr class="click" onclick={() => go('client/' + encodeURIComponent(c.mac))}>
            <td><b>{display(c)}</b><div class="muted mono">{c.ip} · {c.mac}</div></td>
            <td class="r">{#if c.wifi}<span class="badge acc">{c.ssid || 'wifi'}</span>{:else}<span class="badge">{c.iface || 'wired'}</span>{/if}</td></tr>
        {/each}
      </tbody></table>{#if !x.clients.length}<div class="empty">No clients</div>{/if}</div></div>
    <div class="card"><div class="card-h"><h2>Neighbors (LLDP/MNDP)</h2></div>
      <div class="scroll"><table><tbody>
        {#each x.neighbors as n}
          <tr><td><b>{n.ident || n.mac}</b><div class="muted mono">{n.addr} · {n.mac}</div></td><td class="r muted">{n.iface}<br>{n.platform}</td></tr>
        {/each}
      </tbody></table>{#if !x.neighbors.length}<div class="empty">No neighbors seen</div>{/if}</div></div>
  </div>
  {#if x.source === 'ui'}
    <div class="card" style="margin-top:16px"><div class="card-h"><h2>Änderungen auf dem Router</h2>
      <div style="display:flex;gap:8px"><a class="btn sm" href="/api/devices/{encodeURIComponent(name)}/offboard-script" target="_blank" rel="noopener">Rückbau-Befehle</a>
        <button class="btn sm" onclick={() => { off = true; oRes = null; oErr = '' }}>Offboarding …</button></div></div>
      {#if x.managed && man.data?.length}
        <div class="scroll"><table><thead><tr><th>#</th><th>Änderung</th><th>Pfad</th><th>Zeit</th><th>Status</th></tr></thead>
          <tbody>{#each man.data as m (m.id)}<tr><td class="muted">{m.seq}</td><td>{m.descr}</td><td class="mono muted">{m.path}{m.rid ? '/' + m.rid : ''}</td><td class="muted">{datetime(m.ts)}</td><td><span class="badge {sCls[m.state]}">{m.state}</span></td></tr>{/each}</tbody></table></div>
      {:else}<div class="empty">mtmon hat an diesem Gerät nichts geändert – es wird nur gelesen.</div>{/if}
    </div>
  {:else}
    <div class="card card-b muted" style="margin-top:16px">Dieses Gerät ist in <span class="mono">config.json</span> definiert und wird nur gelesen. Entfernen: Eintrag aus der Datei löschen.</div>
  {/if}
{/if}

{#if off}
  <Modal title="Offboarding: {name}" onclose={() => (off = false)}>
    <p style="margin-top:0">Was passiert:</p>
    <ol class="steps">
      <li>mtmon meldet sich <b>einmalig</b> mit einem Admin-Zugang am Router an (wird nicht gespeichert).</li>
      <li>Alle {man.data?.length || 0} protokollierten Änderungen werden in <b>umgekehrter Reihenfolge</b> zurückgebaut (Traffic-Flow-Ziel, Syslog, Log-Einstellungen der Firewall-Regeln auf den alten Wert, Read-only-Benutzer, Gruppe).</li>
      <li>Objekte, die inzwischen von jemand anderem verändert wurden, werden <b>nicht</b> angefasst und gemeldet.</li>
      <li>Danach wird das Gerät aus mtmon entfernt. Die gesammelten Statistiken bleiben erhalten (außer du wählst Löschen).</li>
    </ol>
    {#if x.managed}
      <div class="row"><label>Admin-Benutzer<input class="input" bind:value={oUser} autocomplete="off" /></label><label>Passwort<input class="input" type="password" bind:value={oPass} autocomplete="new-password" /></label></div>
    {/if}
    <label class="chk"><input type="checkbox" bind:checked={oPurge} /> Auch gespeicherte Metriken/Firewall-Events dieses Geräts löschen</label>
    {#if oErr}<div class="warnbox" style="margin-top:12px">{oErr}</div>{/if}
    {#if oRes}
      <div class="warnbox" style="margin-top:12px;background:{oRes.ok ? 'var(--ok-bg)' : 'var(--bad-bg)'}">
        {oRes.ok ? 'Alles zurückgebaut.' : 'Nicht alles konnte zurückgebaut werden – Gerät bleibt eingetragen. Wiederholen oder Befehle manuell ausführen.'}
        {#each oRes.rollback || [] as s}<div style="font-size:12.5px"><span class="badge {s.ok ? 'ok' : 'bad'}">{s.ok ? '✓' : '✗'}</span> {s.title}{s.msg ? ' – ' + s.msg : ''}</div>{/each}
      </div>
      {#if oRes.manual_script}<pre class="code" style="margin-top:10px">{oRes.manual_script}</pre>{/if}
    {/if}
    {#snippet footer()}
      <button class="btn" onclick={() => (off = false)}>Abbrechen</button>
      <button class="btn" disabled={oBusy} title="Entfernt das Gerät nur aus mtmon, der Router bleibt wie er ist" onclick={() => offboard('forget')}>Nur vergessen</button>
      <button class="btn primary" disabled={oBusy || (x.managed && (!oUser || !oPass))} onclick={() => offboard('rollback')}>{oBusy ? 'Baue zurück …' : 'Zurückbauen & entfernen'}</button>
    {/snippet}
  </Modal>
{/if}

<style>
  .caps { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 14px; } .caps .l { display: block; color: var(--muted); font-size: 12px; } .caps b { font-weight: 500; font-size: 13px; word-break: break-word; }
  .steps { padding-left: 20px; display: grid; gap: 6px; margin: 0 0 14px; } .row { display: flex; gap: 12px; margin-bottom: 12px; } .row label { flex: 1; display: grid; gap: 4px; font-weight: 500; font-size: 13px; }
  label.chk { display: flex; gap: 8px; align-items: center; }
  .head { display: flex; align-items: center; gap: 14px; margin-bottom: 18px; flex-wrap: wrap; }
</style>
