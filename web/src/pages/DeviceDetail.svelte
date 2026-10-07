<script>
  import { api } from '../lib/api.js'
  import { t, tr } from '../lib/i18n.svelte.js'
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
      if (oRes.ok) { toast(t('Device removed')); setTimeout(() => go('devices'), 1200) }
    } catch (e) { oErr = e.message } finally { oBusy = false }
  }
  const sCls = { applied: 'ok', reverted: '', 'revert-failed': 'bad' }
  const sLbl = $derived({ applied: t('applied'), reverted: t('reverted'), 'revert-failed': t('revert failed') })
</script>

<div class="head"><a href="#/devices">← {t('Devices')}</a><h1>{name}</h1>
  {#if x}<span class="badge">{x.role}</span><span class="mono muted">{x.addr}</span>{/if}</div>
{#if d.error}<div class="warnbox">{tr(d.error)}</div>{/if}

{#if x}
  <div class="grid g4" style="margin-bottom:16px">
    <div class="card card-b kpi"><div class="l">{t('Model')}</div><div class="v" style="font-size:18px">{x.state?.model || '—'}</div><div class="l">{x.state?.identity}</div></div>
    <div class="card card-b kpi"><div class="l">RouterOS</div><div class="v" style="font-size:18px">{x.state?.version || '—'}</div><div class="l">{x.state?.info?.upgrade && x.state.info.upgrade !== x.state.info.firmware ? t('firmware upgrade available') : t('firmware current')}</div></div>
    <div class="card card-b kpi"><div class="l">{t('Clients')}</div><div class="v num">{x.clients.length}</div><div class="l">{t('online via this device')}</div></div>
    <div class="card card-b kpi"><div class="l">{t('Status')}</div><div class="v" style="font-size:18px"><span class="dot" class:ok={app.live.devices.find(v => v.name === name)?.up} class:bad={!app.live.devices.find(v => v.name === name)?.up}></span>
      {app.live.devices.find(v => v.name === name)?.up ? ' ' + t('Online') : ' ' + t('Offline')}</div><div class="l">{tr(x.state?.last_err) || ''}</div></div>
  </div>

  {#if caps}
    <div class="card" style="margin-bottom:16px"><div class="card-h"><h2>{t('Capabilities')}</h2><span class="muted" style="font-size:12.5px">{t('detected automatically when added')}</span></div>
      <div class="card-b caps">
        <div><span class="l">{t('Role')}</span><b>{caps.role}</b></div>
        <div><span class="l">{t('Wi-Fi')}</span><b>{caps.wifi_stack === 'wifi' ? caps.wifi_ifs.map(w => w.name + (w.ssid ? ' “' + w.ssid + '”' : '') + (w.band ? ' ' + w.band : '')).join(', ') : caps.wifi_stack === 'wireless' ? t('Legacy') : '—'}</b></div>
        <div><span class="l">{t('Network')}</span><b>{caps.dhcp_server ? 'DHCP' : '—'} · {caps.nat ? 'NAT' : t('no NAT')} · {t('{n} bridge(s)', { n: caps.bridges?.length || 0 })}{caps.hw_offload ? ' · ' + t('HW offload') : ''}</b></div>
        <div><span class="l">Firewall</span><b>{t('{n} rules', { n: caps.filter_rules?.length || 0 })}{caps.fasttrack ? ' · FastTrack' : ''}</b></div>
        <div><span class="l">{t('Hardware')}</span><b>{[caps.arch, caps.cpu_count ? t('{n} cores', { n: caps.cpu_count }) : '', caps.mem_total ? Math.round(caps.mem_total / 1048576) + ' MB' : ''].filter(Boolean).join(' · ') || '—'}</b></div>
        <div><span class="l">{t('Packages')}</span><b>{(caps.packages || []).join(', ')}</b></div>
      </div></div>
  {/if}

  <div class="grid g2">
    <div class="card"><div class="card-h"><h2>{t('CPU')}</h2></div><div class="card-b"><Chart series={cpu} fmt={v => v.toFixed(0) + '%'} height={150} empty={t('Collecting metrics (1 sample/min)…')} /></div></div>
    <div class="card"><div class="card-h"><h2>{t('Memory')}</h2></div><div class="card-b"><Chart series={mem} fmt={v => v.toFixed(0) + '%'} height={150} empty={t('Collecting metrics (1 sample/min)…')} /></div></div>
  </div>

  <div class="card" style="margin-top:16px">
    <div class="card-h"><h2>{t('Interfaces')}</h2><button class="btn sm" onclick={() => (showAll = !showAll)}>{showAll ? t('Hide idle') : t('Show all')}</button></div>
    <div class="scroll"><table>
      <thead><tr><th>{t('Name')}</th><th>{t('Type')}</th><th>{t('Link')}</th><th class="r">↓ RX</th><th class="r">↑ TX</th></tr></thead>
      <tbody>
        {#each ifaces.filter(i => showAll || i.rx_bps + i.tx_bps > 0) as i (i.iface)}
          <tr><td><b>{i.iface}</b></td><td class="muted">{i.type}</td><td><span class="dot" class:ok={i.running} class:bad={!i.running}></span> {i.running ? t('up') : t('down')}</td>
            <td class="r num">{bps(i.rx_bps)}</td><td class="r num">{bps(i.tx_bps)}</td></tr>
        {/each}
      </tbody></table>
      {#if !ifaces.length}<div class="empty">{t('No interface data yet')}</div>{/if}
    </div>
  </div>

  <div class="grid g2" style="margin-top:16px">
    <div class="card"><div class="card-h"><h2>{t('Connected clients ({n})', { n: x.clients.length })}</h2></div>
      <div class="scroll"><table><tbody>
        {#each x.clients as c (c.mac)}
          <tr class="click" onclick={() => go('client/' + encodeURIComponent(c.mac))}>
            <td><b>{display(c)}</b><div class="muted mono">{c.ip} · {c.mac}</div></td>
            <td class="r">{#if c.wifi}<span class="badge acc">{c.ssid || 'Wi-Fi'}</span>{:else}<span class="badge">{c.iface || t('wired')}</span>{/if}</td></tr>
        {/each}
      </tbody></table>{#if !x.clients.length}<div class="empty">{t('No clients')}</div>{/if}</div></div>
    <div class="card"><div class="card-h"><h2>{t('Neighbors (LLDP/MNDP)')}</h2></div>
      <div class="scroll"><table><tbody>
        {#each x.neighbors as n}
          <tr><td><b>{n.ident || n.mac}</b><div class="muted mono">{n.addr} · {n.mac}</div></td><td class="r muted">{n.iface}<br>{n.platform}</td></tr>
        {/each}
      </tbody></table>{#if !x.neighbors.length}<div class="empty">{t('No neighbors seen')}</div>{/if}</div></div>
  </div>
  {#if x.source === 'ui'}
    <div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('Changes on the router')}</h2>
      <div style="display:flex;gap:8px"><a class="btn sm" href="/api/devices/{encodeURIComponent(name)}/offboard-script" target="_blank" rel="noopener">{t('Undo commands')}</a>
        <button class="btn sm" onclick={() => { off = true; oRes = null; oErr = '' }}>{t('Offboarding …')}</button></div></div>
      {#if x.managed && man.data?.length}
        <div class="scroll"><table><thead><tr><th>#</th><th>{t('Change')}</th><th>{t('Path')}</th><th>{t('Time')}</th><th>{t('Status')}</th></tr></thead>
          <tbody>{#each man.data as m (m.id)}<tr><td class="muted">{m.seq}</td><td>{tr(m.descr)}</td><td class="mono muted">{m.path}{m.rid ? '/' + m.rid : ''}</td><td class="muted">{datetime(m.ts)}</td><td><span class="badge {sCls[m.state]}">{sLbl[m.state] || m.state}</span></td></tr>{/each}</tbody></table></div>
      {:else}<div class="empty">{t('mtmon has not changed anything on this device – it is read-only.')}</div>{/if}
    </div>
  {:else}
    <div class="card card-b muted" style="margin-top:16px">{t('This device is defined in {f} and is read-only. To remove it, delete the entry from the file.', { f: 'config.json' })}</div>
  {/if}
{/if}

{#if off}
  <Modal title={t('Offboarding: {name}', { name })} onclose={() => (off = false)}>
    <p style="margin-top:0">{t('What happens:')}</p>
    <ol class="steps">
      <li>{t('mtmon signs in to the router')} <b>{t('once')}</b> {t('with admin credentials (not stored).')}</li>
      <li>{t('All {n} recorded changes are undone in', { n: man.data?.length || 0 })} <b>{t('reverse order')}</b> {t('(traffic flow target, syslog, firewall rule log settings back to the old value, read-only user, group).')}</li>
      <li>{t('Objects that have been changed by someone else in the meantime are')} <b>{t('not')}</b> {t('touched and are reported.')}</li>
      <li>{t('Afterwards the device is removed from mtmon. The collected statistics are kept (unless you choose to delete them).')}</li>
    </ol>
    {#if x.managed}
      <div class="row"><label>{t('Admin user')}<input class="input" bind:value={oUser} autocomplete="off" /></label><label>{t('Password')}<input class="input" type="password" bind:value={oPass} autocomplete="new-password" /></label></div>
    {/if}
    <label class="chk"><input type="checkbox" bind:checked={oPurge} /> {t('Also delete this device’s stored metrics/firewall events')}</label>
    {#if oErr}<div class="warnbox" style="margin-top:12px">{tr(oErr)}</div>{/if}
    {#if oRes}
      <div class="warnbox" style="margin-top:12px;background:{oRes.ok ? 'var(--ok-bg)' : 'var(--bad-bg)'}">
        {oRes.ok ? t('Everything undone.') : t('Not everything could be undone – the device stays registered. Retry or run the commands manually.')}
        {#each oRes.rollback || [] as s}<div style="font-size:12.5px"><span class="badge {s.ok ? 'ok' : 'bad'}">{s.ok ? '✓' : '✗'}</span> {tr(s.title)}{s.msg ? ' – ' + tr(s.msg) : ''}</div>{/each}
      </div>
      {#if oRes.manual_script}<pre class="code" style="margin-top:10px">{oRes.manual_script}</pre>{/if}
    {/if}
    {#snippet footer()}
      <button class="btn" onclick={() => (off = false)}>{t('Cancel')}</button>
      <button class="btn" disabled={oBusy} title={t('Removes the device from mtmon only; the router stays as it is')} onclick={() => offboard('forget')}>{t('Just forget')}</button>
      <button class="btn primary" disabled={oBusy || (x.managed && (!oUser || !oPass))} onclick={() => offboard('rollback')}>{oBusy ? t('Undoing …') : t('Undo & remove')}</button>
    {/snippet}
  </Modal>
{/if}

<style>
  .caps { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 14px; } .caps .l { display: block; color: var(--muted); font-size: 12px; } .caps b { font-weight: 500; font-size: 13px; word-break: break-word; }
  .steps { padding-left: 20px; display: grid; gap: 6px; margin: 0 0 14px; } .row { display: flex; gap: 12px; margin-bottom: 12px; } .row label { flex: 1; display: grid; gap: 4px; font-weight: 500; font-size: 13px; }
  label.chk { display: flex; gap: 8px; align-items: center; }
  .head { display: flex; align-items: center; gap: 14px; margin-bottom: 18px; flex-wrap: wrap; }
</style>
