<script>
  import { api } from '../lib/api.js'
  import { t, tr } from '../lib/i18n.svelte.js'
  import { go, toast } from '../lib/state.svelte.js'
  import Modal from '../lib/Modal.svelte'
  let { onclose, ondone } = $props()

  let step = $state('connect')        // connect | review | running | done
  let f = $state({ addr: '', port: 443, user: 'admin', pass: '', mode: 'auto' })
  let busy = $state(false), err = $state('')
  let pr = $state(null)               // probe result
  let opt = $state(null)              // editable options
  let plan = $state([])
  let fpOk = $state(false)
  let result = $state(null)
  let open = $state({})               // expanded plan steps

  // --- requirements checklist (shown before connecting) ---
  const rIP = $derived(f.addr.trim() || '<ROUTER-IP>')
  const mIP = typeof location !== 'undefined' ? location.hostname : '<MTMON-IP>'
  const reqs = $derived([
    { id: 'cert', title: t('HTTPS service www-ssl with certificate'), why: t('mtmon talks to the REST API over HTTPS (port 443). Without a certificate the TLS handshake fails (“tls: handshake failure”).'),
      cmd: `/certificate add name=mtmon-ca common-name=mtmon-ca days-valid=3650 key-usage=key-cert-sign,crl-sign
/certificate sign mtmon-ca
/certificate add name=mtmon-ssl common-name=${rIP} subject-alt-name=IP:${rIP} days-valid=3650 key-usage=digital-signature,key-encipherment,tls-server
/certificate sign mtmon-ssl ca=mtmon-ca
/ip service set www-ssl certificate=mtmon-ssl disabled=no address=<LAN-NETZ>/24` },
    { id: 'fw', title: t('Firewall: allow mtmon to reach the router'), why: t('If the router has a “drop all not from LAN” rule (input chain), mtmon must be allowed before it. Move the rule to the top.'),
      cmd: `/ip firewall filter add chain=input action=accept protocol=tcp dst-port=443 src-address=${mIP} comment="mtmon REST" place-before=0` },
    { id: 'user', title: t('Admin access for the setup'), why: t('One-time login with group full (or write + api + rest-api + policy). mtmon creates its own read-only user and does not store the admin login.'),
      cmd: `/user print where name=<ADMIN-USER>` },
    { id: 'net', title: t('Network: routers can reach mtmon (UDP)'), why: t('Traffic Flow goes via UDP 2055, firewall syslog via UDP 5514 from the router to mtmon. If mtmon is in a different network, the firewall must allow this.'),
      cmd: `/ip firewall filter add chain=forward action=accept protocol=udp dst-address=${mIP} dst-port=2055,5514 comment="mtmon flows+syslog" place-before=0` },
  ])
  let reqOpen = $state(false)
  async function copy(t) { try { await navigator.clipboard.writeText(t); toast?.(t('Copied')) } catch { toast?.(t('Copy failed – select the text manually')) } }
  $effect(() => { if (err && /TLS|certificate|Zertifikat|refused|abgelehnt|timeout|timed out|Zeitüberschreitung|www-ssl/i.test(err)) reqOpen = true })

  const caps = $derived(pr?.caps)
  const risk = $derived({ none: ['ok', t('no impact')], low: ['', t('low')], medium: ['warn', t('medium')] })

  async function probe() {
    busy = true; err = ''
    try {
      pr = await api('/devices/probe', { method: 'POST', body: { addr: f.addr, port: +f.port || 443, user: f.user, pass: f.pass } })
      opt = JSON.parse(JSON.stringify(pr.options)); plan = pr.plan; fpOk = false; step = 'review'
    } catch (e) { err = tr(e.message) } finally { busy = false }
  }

  // re-explain whenever options change
  $effect(() => {
    if (!opt || !pr || f.mode !== 'auto') return
    const o = JSON.parse(JSON.stringify(opt))
    const tm = setTimeout(async () => { try { plan = await api('/devices/plan', { method: 'POST', body: { caps: pr.caps, options: o } }) } catch {} }, 250)
    return () => clearTimeout(tm)
  })

  function toggleRule(id) { opt.fw_rules = opt.fw_rules?.includes(id) ? opt.fw_rules.filter(x => x !== id) : [...(opt.fw_rules || []), id] }

  async function apply() {
    busy = true; err = ''; step = 'running'
    try {
      if (f.mode === 'readonly') {
        await api('/devices/readonly', { method: 'POST', body: { addr: f.addr, port: +f.port || 443, user: f.user, pass: f.pass, fingerprint: pr.fingerprint, name: opt.name, role: opt.role, site: opt.site } })
        result = { result: { ok: true, steps: [{ title: t('Device added (read-only)'), ok: true }] }, name: opt.name }
      } else {
        result = await api('/devices/provision', { method: 'POST', body: { addr: f.addr, port: +f.port || 443, user: f.user, pass: f.pass, fingerprint: pr.fingerprint, options: opt } })
      }
      f.pass = ''
      step = 'done'; ondone?.()
    } catch (e) { err = tr(e.message); step = 'review' } finally { busy = false }
  }
  const ok = $derived(result?.result?.ok)
</script>

<Modal title={step === 'connect' ? t('Add device') : step === 'review' ? t('Review device & confirm setup') : step === 'running' ? t('Setting up …') : ok ? t('Done') : t('Setup failed')} wide={step !== 'connect'} {onclose}>
  {#if step === 'connect'}
    <p class="muted" style="margin-top:0">{t('Enter the address and an')} <b>{t('admin account')}</b> {t('of the MikroTik. mtmon reads the device’s capabilities, shows you exactly what it would set up, and only changes something after your confirmation.')}</p>
    <details class="req" bind:open={reqOpen}>
      <summary><b>{t('Requirements on the router')}</b> <span class="muted">· {t('check once before you connect')}</span></summary>
      <p class="muted" style="margin:8px 0">{t('The commands apply to RouterOS 7 (terminal or WinBox → New Terminal). You replace placeholders in')} <span class="mono">&lt;…&gt;</span>. {t('The address below is inserted automatically.')}</p>
      {#each reqs as r, i (r.id)}
        <div class="step">
          <div class="sh" style="cursor:default"><span class="n">{i + 1}</span><b>{r.title}</b>
            <button type="button" class="btn sm" style="margin-left:auto" onclick={() => copy(r.cmd)}>{t('Copy')}</button></div>
          <div class="why">{r.why}</div>
          <pre class="code">{r.cmd}</pre>
        </div>
      {/each}
    </details>
    <form class="form" onsubmit={e => { e.preventDefault(); probe() }}>
      <div class="row"><label style="flex:1">{t('Address (IP or hostname)')}<input class="input mono" bind:value={f.addr} placeholder="192.168.88.1" required autocomplete="off" /></label>
        <label style="width:110px">{t('REST port')}<input class="input mono" type="number" bind:value={f.port} /></label></div>
      <div class="row"><label style="flex:1">{t('User')}<input class="input" bind:value={f.user} autocomplete="off" required /></label>
        <label style="flex:1">{t('Password')}<input class="input" type="password" bind:value={f.pass} autocomplete="new-password" required /></label></div>
      <div class="tabs" style="justify-self:start" role="tablist">
        <button type="button" role="tab" aria-selected={f.mode === 'auto'} class:on={f.mode === 'auto'} onclick={() => (f.mode = 'auto')}>{t('Auto setup (recommended)')}</button>
        <button type="button" role="tab" aria-selected={f.mode === 'readonly'} class:on={f.mode === 'readonly'} onclick={() => (f.mode = 'readonly')}>{t('Monitor only')}</button>
      </div>
      {#if f.mode === 'auto'}
        <div class="info">{t('The admin account is used')} <b>{t('once')}</b> {t('and')} <b>{t('not stored')}</b>. {t('mtmon creates its own read-only user for this and records every change so it can later be fully undone via offboarding.')}</div>
      {:else}
        <div class="info">{t('Nothing is changed on the router. You enter an existing user (ideally with read/api/rest-api only). You then have to set up Traffic Flow and syslog yourself (Settings → Router setup).')}</div>
      {/if}
      {#if err}<div class="warnbox">{err}</div>{/if}
      <button class="btn primary" style="justify-self:start" disabled={busy}>{busy ? t('Checking …') : t('Connect & check')}</button>
    </form>

  {:else if step === 'review' && pr}
    <div class="grid g2" style="gap:14px">
      <div class="box">
        <h3>{caps.model || 'MikroTik'} <span class="muted">· RouterOS {caps.version}</span></h3>
        <div class="kv"><span>Identity</span><b>{caps.identity || '—'}</b></div>
        <div class="kv"><span>{t('Architecture / CPU')}</span><b>{[caps.arch, caps.cpu_count ? t('{n} cores', { n: caps.cpu_count }) : '', caps.mem_total ? Math.round(caps.mem_total / 1048576) + ' MB RAM' : ''].filter(Boolean).join(' · ') || '—'}</b></div>
        <div class="kv"><span>{t('Detected role')}</span><b>{caps.role} <span class="muted">({tr(caps.role_why)})</span></b></div>
        <div class="kv"><span>{t('Wi-Fi')}</span><b>{#if caps.wifi_stack === 'wifi'}{t('wifi package')} · {caps.wifi_ifs.map(w => w.name + (w.ssid ? ' “' + w.ssid + '”' : '') + (w.band ? ' ' + w.band : '')).join(', ')}{:else if caps.wifi_stack === 'wireless'}{t('Legacy wireless')}{:else}{t('none')}{/if}</b></div>
        <div class="kv"><span>{t('Network')}</span><b>{caps.dhcp_server ? t('DHCP server') : t('no DHCP')} · {caps.nat ? 'NAT' : t('no NAT')} · {t('{n} bridge(s)', { n: caps.bridges?.length || 0 })}{caps.hw_offload ? ' · ' + t('HW offload') : ''}</b></div>
        <div class="kv"><span>Traffic Flow</span><b>{caps.traffic_flow.supported ? (caps.traffic_flow.enabled ? t('active') : t('off')) : t('not supported')}{#if caps.traffic_flow.targets?.length} · {t('Targets:')} {caps.traffic_flow.targets.join(', ')}{/if}</b></div>
        <div class="kv"><span>Firewall</span><b>{t('{n} filter rules', { n: caps.filter_rules?.length || 0 })}{caps.fasttrack ? ' · ' + t('FastTrack active') : ''}</b></div>
        <div class="kv"><span>REST (HTTPS)</span><b>{caps.www_ssl ? t('active') : t('OFF')}{caps.www_ssl_address ? ' · ' + t('restricted to') + ' ' + caps.www_ssl_address : ''}</b></div>
        <div class="chips">{#each caps.packages || [] as p}<span class="badge">{p}</span>{/each}</div>
      </div>
      <div class="box">
        <h3>{t('Certificate')}</h3>
        <p class="muted" style="margin:0 0 6px">{t('Fingerprint (SHA-256) of the router. mtmon pins it for all further connections. Compare it with')} <span class="mono">/certificate print</span> {t('if needed.')}</p>
        <div class="mono fp">{pr.fingerprint.match(/.{1,2}/g)?.join(':')}</div>
        <label class="chk"><input type="checkbox" bind:checked={fpOk} /> {t('Matches – trust this device')}</label>
        {#each caps.warnings || [] as w}<div class="warnbox" style="margin-top:10px;font-size:12.5px">{tr(w)}</div>{/each}
        {#if f.mode === 'auto' && !pr.can_write}<div class="warnbox" style="margin-top:10px">{t('This user has no write permissions – auto setup is not possible. Use an admin or choose “Monitor only”.')}</div>{/if}
      </div>
    </div>

    <div class="box" style="margin-top:14px">
      <h3>{t('Settings')}</h3>
      <div class="row">
        <label style="flex:1">{t('Name in mtmon')}<input class="input" bind:value={opt.name} maxlength="40" /></label>
        <label style="width:150px">{t('Role')}<select class="input" bind:value={opt.role}><option value="router">{t('Router')}</option><option value="ap">{t('Access Point')}</option><option value="switch">{t('Switch')}</option></select></label>
        <label style="flex:1">{t('Site')}<input class="input" bind:value={opt.site} maxlength="40" placeholder={t('optional')} /></label>
      </div>
      {#if f.mode === 'auto'}
        <div class="row" style="margin-top:10px">
          <label style="flex:1">{t('mtmon IP as seen by the router')}<input class="input mono" bind:value={opt.mtmon_ip} /></label>
          <label style="width:130px">{t('Flow port (UDP)')}<input class="input mono" type="number" bind:value={opt.flow_port} /></label>
          <label style="width:130px">{t('Syslog port (UDP)')}<input class="input mono" type="number" bind:value={opt.syslog_port} /></label>
        </div>
        <div class="opts">
          <label class="chk"><input type="checkbox" bind:checked={opt.flow} disabled={!caps.traffic_flow.supported} /> <b>Traffic Flow</b> {t('export to mtmon')} <span class="muted">({t('who connects where, ports, bytes')})</span></label>
          <label class="chk"><input type="checkbox" bind:checked={opt.syslog} /> <b>{t('Firewall logs')}</b> {t('send via syslog')} <span class="muted">({t('which rule blocked')})</span></label>
          {#if caps.fasttrack}<label class="chk"><input type="checkbox" bind:checked={opt.disable_fasttrack} /> {t('Disable FastTrack')} <span class="muted">({t('complete numbers, more router CPU')})</span></label>{/if}
          {#if opt.syslog}<label class="chk"><input type="checkbox" bind:checked={opt.log_new} /> {t('Log new connections at the end of the forward chain')} <span class="muted">({t('evidence of “allowed”, more log volume')})</span></label>{/if}
        </div>
        {#if opt.syslog && caps.filter_rules?.length}
          <h3 style="margin-top:14px">{t('Which firewall rules should have logging enabled?')}</h3>
          <div class="scroll" style="max-height:230px;overflow-y:auto"><table>
            <thead><tr><th></th><th>Chain</th><th>{t('Action')}</th><th>{t('Rule')}</th></tr></thead>
            <tbody>{#each caps.filter_rules.filter(r => r.chain && !r.managed) as r (r.id)}
              <tr><td><input type="checkbox" checked={opt.fw_rules?.includes(r.id)} onchange={() => toggleRule(r.id)} disabled={r.disabled} aria-label={t('Logging for rule {id}', { id: r.id })} /></td>
                <td>{r.chain}</td><td><span class="badge" class:bad={r.action === 'drop' || r.action === 'reject'}>{r.action}</span></td>
                <td><b>{r.comment || '—'}</b><div class="muted mono" style="font-size:11.5px">{r.summary}{r.disabled ? ' · ' + t('disabled') : ''}{r.log ? ' · ' + t('already logging') : ''}</div></td></tr>
            {/each}</tbody></table></div>
          <p class="muted" style="font-size:12.5px;margin:6px 0 0">{t('Preselected: all active drop/reject rules. Only')} <span class="mono">log</span> {t('and')} <span class="mono">log-prefix</span> {t('are changed.')}</p>
        {/if}
      {/if}
    </div>

    {#if f.mode === 'auto'}
      <div class="box" style="margin-top:14px">
        <h3>{t('What mtmon changes on the router')} <span class="muted">({t('{n} steps', { n: plan.length })})</span></h3>
        <p class="muted" style="margin:0 0 10px">{t('Every step is recorded and can be undone via offboarding. If any step fails, mtmon automatically undoes everything again.')}</p>
        {#each plan as s, i (s.key)}
          <div class="step">
            <button class="sh" onclick={() => (open[s.key] = !open[s.key])} aria-expanded={!!open[s.key]}>
              <span class="n">{i + 1}</span><b>{tr(s.title)}</b><span class="badge {risk[s.risk]?.[0]}">{t('Risk')}: {risk[s.risk]?.[1]}</span><span class="chev">{open[s.key] ? '▾' : '▸'}</span>
            </button>
            <div class="why">{tr(s.why)}</div>
            {#if open[s.key]}
              <pre class="code">{s.commands.join('\n')}</pre>
              <div class="muted" style="font-size:12.5px">↩ {t('Undo')}: {tr(s.undo)}</div>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
    {#if err}<div class="warnbox" style="margin-top:12px">{err}</div>{/if}

  {:else if step === 'running'}
    <div class="empty"><span class="dot pulse"></span> {t('Setting up {name} … (backup, user, Traffic Flow, syslog, test)', { name: opt.name })}</div>

  {:else if step === 'done' && result}
    <div class="warnbox" style="background:{ok ? 'var(--ok-bg)' : 'var(--bad-bg)'};border-color:{ok ? 'var(--ok)' : 'var(--bad)'}">
      {#if ok}<b>{t('{name} is set up.', { name: opt.name })}</b> {opt.syslog ? t('The router now sends flows and firewall logs to mtmon.') : t('The router now sends flows to mtmon.')} {t('The first data appears after a few seconds.')}
      {:else}<b>{t('Setup failed.')}</b> {tr(result.result.error)}
        {#if result.result.rolled_back}<br>{t('mtmon has')} <b>{t('automatically undone')}</b> {t('all changes already made.')}{/if}{/if}
    </div>
    <div class="steps">
      {#each result.result.steps as s}<div class="srow"><span class="badge {s.ok ? 'ok' : 'bad'}">{s.ok ? '✓' : '✗'}</span> {tr(s.title)}{#if s.msg}<div class="muted" style="font-size:12.5px">{tr(s.msg)}</div>{/if}</div>{/each}
      {#each result.result.rollback || [] as s}<div class="srow"><span class="badge {s.ok ? 'ok' : 'bad'}">↩</span> {tr(s.title)}{#if s.msg}<div class="muted" style="font-size:12.5px">{tr(s.msg)}</div>{/if}</div>{/each}
    </div>
    {#if result.result.backup}<p class="muted">{t('Config backup on the router:')} <span class="mono">{result.result.backup}</span> ({t('Files')})</p>{/if}
    {#if result.manual_script}<p><b>{t('The undo was incomplete.')}</b> {t('Run these commands in the RouterOS terminal:')}</p><pre class="code">{result.manual_script}</pre>{/if}
  {/if}

  {#snippet footer()}
    {#if step === 'review'}
      <button class="btn" onclick={() => { step = 'connect'; err = '' }}>{t('Back')}</button>
      <button class="btn primary" disabled={busy || !fpOk || !opt.name || (f.mode === 'auto' && !pr.can_write)} onclick={apply}>
        {f.mode === 'auto' ? t('Set up now') : t('Add')}</button>
    {:else if step === 'done'}
      {#if ok}<button class="btn primary" onclick={() => { onclose(); go('device/' + encodeURIComponent(result.name)) }}>{t('Go to device')}</button>
      {:else}<button class="btn" onclick={() => { step = 'review'; result = null }}>{t('Back to review')}</button>{/if}
      <button class="btn" onclick={onclose}>{t('Close')}</button>
    {/if}
  {/snippet}
</Modal>

<style>
  .form { display: grid; gap: 14px; } label { display: grid; gap: 4px; font-weight: 500; font-size: 13px; }
  .row { display: flex; gap: 12px; flex-wrap: wrap; align-items: end; }
  .info { padding: 10px 12px; background: var(--accent-bg); border-radius: 8px; font-size: 13px; }
  .box { border: 1px solid var(--border); border-radius: 10px; padding: 14px 16px; background: var(--card-2); }
  h3 { font-size: 14px; margin: 0 0 10px; font-weight: 600; }
  .kv { display: flex; justify-content: space-between; gap: 12px; padding: 4px 0; border-bottom: 1px dashed var(--border); font-size: 13px; }
  .kv:last-of-type { border-bottom: 0; } .kv span { color: var(--muted); flex-shrink: 0; } .kv b { text-align: right; font-weight: 500; }
  .chips { display: flex; flex-wrap: wrap; gap: 5px; margin-top: 10px; }
  .fp { word-break: break-all; padding: 8px 10px; background: var(--card); border: 1px solid var(--border); border-radius: 8px; margin-bottom: 8px; }
  label.chk { display: flex; gap: 8px; align-items: flex-start; font-weight: 400; margin-top: 6px; } label.chk input { margin-top: 3px; }
  .opts { margin-top: 12px; display: grid; gap: 2px; }
  .req { border: 1px solid var(--border); border-radius: 10px; padding: 10px 14px; margin: 0 0 14px; }
  .req summary { cursor: pointer; }
  .req pre.code { white-space: pre-wrap; word-break: break-all; }
  .btn.sm { padding: 3px 10px; font-size: 12px; }
  .step { padding: 10px 0; border-top: 1px solid var(--border); } .step:first-of-type { border-top: 0; }
  .sh { display: flex; align-items: center; gap: 10px; width: 100%; background: none; border: 0; padding: 0; text-align: left; }
  .n { width: 22px; height: 22px; border-radius: 50%; background: var(--accent-bg); color: var(--accent-strong); display: grid; place-items: center; font-size: 12px; font-weight: 600; flex-shrink: 0; }
  .chev { margin-left: auto; color: var(--muted); }
  .why { margin: 4px 0 0 32px; color: var(--muted); font-size: 13px; }
  .step pre, .step > div:last-child { margin-left: 32px; margin-top: 8px; }
  .steps { margin-top: 14px; display: grid; gap: 8px; } .srow { display: flex; gap: 10px; align-items: baseline; flex-wrap: wrap; }
</style>
