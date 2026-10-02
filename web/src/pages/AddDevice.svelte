<script>
  import { api } from '../lib/api.js'
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
    { id: 'cert', title: 'HTTPS-Dienst www-ssl mit Zertifikat', why: 'mtmon spricht die REST-API per HTTPS (Port 443). Ohne Zertifikat bricht der TLS-Handshake ab („tls: handshake failure“).',
      cmd: `/certificate add name=mtmon-ca common-name=mtmon-ca days-valid=3650 key-usage=key-cert-sign,crl-sign
/certificate sign mtmon-ca
/certificate add name=mtmon-ssl common-name=${rIP} subject-alt-name=IP:${rIP} days-valid=3650 key-usage=digital-signature,key-encipherment,tls-server
/certificate sign mtmon-ssl ca=mtmon-ca
/ip service set www-ssl certificate=mtmon-ssl disabled=no address=<LAN-NETZ>/24` },
    { id: 'fw', title: 'Firewall: mtmon darf auf den Router', why: 'Hat der Router eine „drop all not from LAN“-Regel (Input-Chain), muss mtmon vorher erlaubt sein. Die Regel nach oben schieben.',
      cmd: `/ip firewall filter add chain=input action=accept protocol=tcp dst-port=443 src-address=${mIP} comment="mtmon REST" place-before=0` },
    { id: 'user', title: 'Admin-Zugang für die Einrichtung', why: 'Einmaliger Login mit Gruppe full (oder write + api + rest-api + policy). mtmon legt einen eigenen Nur-Lese-Benutzer an und speichert den Admin-Login nicht.',
      cmd: `/user print where name=<ADMIN-USER>` },
    { id: 'net', title: 'Netz: Router erreichen mtmon (UDP)', why: 'Traffic Flow geht per UDP 2055, Firewall-Syslog per UDP 5514 vom Router zu mtmon. Liegt mtmon in einem anderen Netz, muss die Firewall das erlauben.',
      cmd: `/ip firewall filter add chain=forward action=accept protocol=udp dst-address=${mIP} dst-port=2055,5514 comment="mtmon flows+syslog" place-before=0` },
  ])
  let reqOpen = $state(false)
  async function copy(t) { try { await navigator.clipboard.writeText(t); toast?.('Kopiert') } catch { toast?.('Kopieren nicht möglich – Text markieren') } }
  $effect(() => { if (err && /TLS|Zertifikat|abgelehnt|Zeitüberschreitung|www-ssl/i.test(err)) reqOpen = true })

  const caps = $derived(pr?.caps)
  const risk = { none: ['ok', 'keine Auswirkung'], low: ['', 'gering'], medium: ['warn', 'mittel'] }

  async function probe() {
    busy = true; err = ''
    try {
      pr = await api('/devices/probe', { method: 'POST', body: { addr: f.addr, port: +f.port || 443, user: f.user, pass: f.pass } })
      opt = JSON.parse(JSON.stringify(pr.options)); plan = pr.plan; fpOk = false; step = 'review'
    } catch (e) { err = e.message } finally { busy = false }
  }

  // re-explain whenever options change
  $effect(() => {
    if (!opt || !pr || f.mode !== 'auto') return
    const o = JSON.parse(JSON.stringify(opt))
    const t = setTimeout(async () => { try { plan = await api('/devices/plan', { method: 'POST', body: { caps: pr.caps, options: o } }) } catch {} }, 250)
    return () => clearTimeout(t)
  })

  function toggleRule(id) { opt.fw_rules = opt.fw_rules?.includes(id) ? opt.fw_rules.filter(x => x !== id) : [...(opt.fw_rules || []), id] }

  async function apply() {
    busy = true; err = ''; step = 'running'
    try {
      if (f.mode === 'readonly') {
        await api('/devices/readonly', { method: 'POST', body: { addr: f.addr, port: +f.port || 443, user: f.user, pass: f.pass, fingerprint: pr.fingerprint, name: opt.name, role: opt.role, site: opt.site } })
        result = { result: { ok: true, steps: [{ title: 'Gerät hinzugefügt (nur lesend)', ok: true }] }, name: opt.name }
      } else {
        result = await api('/devices/provision', { method: 'POST', body: { addr: f.addr, port: +f.port || 443, user: f.user, pass: f.pass, fingerprint: pr.fingerprint, options: opt } })
      }
      f.pass = ''
      step = 'done'; ondone?.()
    } catch (e) { err = e.message; step = 'review' } finally { busy = false }
  }
  const ok = $derived(result?.result?.ok)
</script>

<Modal title={step === 'connect' ? 'Gerät hinzufügen' : step === 'review' ? 'Gerät prüfen & Einrichtung bestätigen' : step === 'running' ? 'Richte ein …' : ok ? 'Fertig' : 'Einrichtung fehlgeschlagen'} wide={step !== 'connect'} {onclose}>
  {#if step === 'connect'}
    <p class="muted" style="margin-top:0">Gib die Adresse und einen <b>Admin-Zugang</b> des MikroTik ein. mtmon liest die Fähigkeiten des Geräts aus, zeigt dir genau, was es einrichten würde, und ändert erst nach deiner Bestätigung etwas.</p>
    <details class="req" bind:open={reqOpen}>
      <summary><b>Voraussetzungen am Router</b> <span class="muted">· einmal prüfen, bevor du verbindest</span></summary>
      <p class="muted" style="margin:8px 0">Die Befehle gelten für RouterOS 7 (Terminal oder WinBox → New Terminal). Platzhalter in <span class="mono">&lt;…&gt;</span> ersetzt du. Die Adresse unten wird automatisch eingesetzt.</p>
      {#each reqs as r, i (r.id)}
        <div class="step">
          <div class="sh" style="cursor:default"><span class="n">{i + 1}</span><b>{r.title}</b>
            <button type="button" class="btn sm" style="margin-left:auto" onclick={() => copy(r.cmd)}>Kopieren</button></div>
          <div class="why">{r.why}</div>
          <pre class="code">{r.cmd}</pre>
        </div>
      {/each}
    </details>
    <form class="form" onsubmit={e => { e.preventDefault(); probe() }}>
      <div class="row"><label style="flex:1">Adresse (IP oder Hostname)<input class="input mono" bind:value={f.addr} placeholder="192.168.88.1" required autocomplete="off" /></label>
        <label style="width:110px">REST-Port<input class="input mono" type="number" bind:value={f.port} /></label></div>
      <div class="row"><label style="flex:1">Benutzer<input class="input" bind:value={f.user} autocomplete="off" required /></label>
        <label style="flex:1">Passwort<input class="input" type="password" bind:value={f.pass} autocomplete="new-password" required /></label></div>
      <div class="tabs" style="justify-self:start" role="tablist">
        <button type="button" role="tab" aria-selected={f.mode === 'auto'} class:on={f.mode === 'auto'} onclick={() => (f.mode = 'auto')}>Auto-Setup (empfohlen)</button>
        <button type="button" role="tab" aria-selected={f.mode === 'readonly'} class:on={f.mode === 'readonly'} onclick={() => (f.mode = 'readonly')}>Nur überwachen</button>
      </div>
      {#if f.mode === 'auto'}
        <div class="info">Der Admin-Zugang wird <b>einmalig</b> benutzt und <b>nicht gespeichert</b>. mtmon legt dafür einen eigenen Read-only-Benutzer an und merkt sich jede Änderung, damit sie später per Offboarding vollständig zurückgebaut werden kann.</div>
      {:else}
        <div class="info">Es wird nichts am Router geändert. Du trägst einen bestehenden Benutzer ein (idealerweise nur mit read/api/rest-api). Traffic Flow und Syslog musst du dann selbst einrichten (Settings → Router setup).</div>
      {/if}
      {#if err}<div class="warnbox">{err}</div>{/if}
      <button class="btn primary" style="justify-self:start" disabled={busy}>{busy ? 'Prüfe …' : 'Verbinden & prüfen'}</button>
    </form>

  {:else if step === 'review' && pr}
    <div class="grid g2" style="gap:14px">
      <div class="box">
        <h3>{caps.model || 'MikroTik'} <span class="muted">· RouterOS {caps.version}</span></h3>
        <div class="kv"><span>Identity</span><b>{caps.identity || '—'}</b></div>
        <div class="kv"><span>Architektur / CPU</span><b>{[caps.arch, caps.cpu_count ? caps.cpu_count + ' Kerne' : '', caps.mem_total ? Math.round(caps.mem_total / 1048576) + ' MB RAM' : ''].filter(Boolean).join(' · ') || '—'}</b></div>
        <div class="kv"><span>Erkannte Rolle</span><b>{caps.role} <span class="muted">({caps.role_why})</span></b></div>
        <div class="kv"><span>WLAN</span><b>{#if caps.wifi_stack === 'wifi'}wifi-Paket · {caps.wifi_ifs.map(w => w.name + (w.ssid ? ' “' + w.ssid + '”' : '') + (w.band ? ' ' + w.band : '')).join(', ')}{:else if caps.wifi_stack === 'wireless'}Legacy-wireless{:else}keins{/if}</b></div>
        <div class="kv"><span>Netz</span><b>{caps.dhcp_server ? 'DHCP-Server' : 'kein DHCP'} · {caps.nat ? 'NAT' : 'kein NAT'} · {caps.bridges?.length || 0} Bridge(s){caps.hw_offload ? ' · HW-Offload' : ''}</b></div>
        <div class="kv"><span>Traffic Flow</span><b>{caps.traffic_flow.supported ? (caps.traffic_flow.enabled ? 'aktiv' : 'aus') : 'nicht unterstützt'}{#if caps.traffic_flow.targets?.length} · Ziele: {caps.traffic_flow.targets.join(', ')}{/if}</b></div>
        <div class="kv"><span>Firewall</span><b>{caps.filter_rules?.length || 0} Filterregeln{caps.fasttrack ? ' · FastTrack aktiv' : ''}</b></div>
        <div class="kv"><span>REST (HTTPS)</span><b>{caps.www_ssl ? 'aktiv' : 'AUS'}{caps.www_ssl_address ? ' · beschränkt auf ' + caps.www_ssl_address : ''}</b></div>
        <div class="chips">{#each caps.packages || [] as p}<span class="badge">{p}</span>{/each}</div>
      </div>
      <div class="box">
        <h3>Zertifikat</h3>
        <p class="muted" style="margin:0 0 6px">Fingerprint (SHA-256) des Routers. mtmon pinnt ihn für alle weiteren Verbindungen. Vergleiche ihn bei Bedarf mit <span class="mono">/certificate print</span>.</p>
        <div class="mono fp">{pr.fingerprint.match(/.{1,2}/g)?.join(':')}</div>
        <label class="chk"><input type="checkbox" bind:checked={fpOk} /> Passt – diesem Gerät vertrauen</label>
        {#each caps.warnings || [] as w}<div class="warnbox" style="margin-top:10px;font-size:12.5px">{w}</div>{/each}
        {#if f.mode === 'auto' && !pr.can_write}<div class="warnbox" style="margin-top:10px">Dieser Benutzer hat keine Schreibrechte – Auto-Setup nicht möglich. Nimm einen Admin oder wähle „Nur überwachen“.</div>{/if}
      </div>
    </div>

    <div class="box" style="margin-top:14px">
      <h3>Einstellungen</h3>
      <div class="row">
        <label style="flex:1">Name in mtmon<input class="input" bind:value={opt.name} maxlength="40" /></label>
        <label style="width:150px">Rolle<select class="input" bind:value={opt.role}><option value="router">Router</option><option value="ap">Access Point</option><option value="switch">Switch</option></select></label>
        <label style="flex:1">Standort<input class="input" bind:value={opt.site} maxlength="40" placeholder="optional" /></label>
      </div>
      {#if f.mode === 'auto'}
        <div class="row" style="margin-top:10px">
          <label style="flex:1">IP von mtmon aus Sicht des Routers<input class="input mono" bind:value={opt.mtmon_ip} /></label>
          <label style="width:130px">Flow-Port (UDP)<input class="input mono" type="number" bind:value={opt.flow_port} /></label>
          <label style="width:130px">Syslog-Port (UDP)<input class="input mono" type="number" bind:value={opt.syslog_port} /></label>
        </div>
        <div class="opts">
          <label class="chk"><input type="checkbox" bind:checked={opt.flow} disabled={!caps.traffic_flow.supported} /> <b>Traffic Flow</b> an mtmon exportieren <span class="muted">(wer verbindet sich wohin, Ports, Bytes)</span></label>
          <label class="chk"><input type="checkbox" bind:checked={opt.syslog} /> <b>Firewall-Logs</b> per Syslog senden <span class="muted">(welche Regel hat geblockt)</span></label>
          {#if caps.fasttrack}<label class="chk"><input type="checkbox" bind:checked={opt.disable_fasttrack} /> FastTrack deaktivieren <span class="muted">(vollständige Zahlen, mehr Router-CPU)</span></label>{/if}
          {#if opt.syslog}<label class="chk"><input type="checkbox" bind:checked={opt.log_new} /> Neue Verbindungen am Ende der forward-Chain loggen <span class="muted">(Beleg „durchgelassen“, mehr Log-Volumen)</span></label>{/if}
        </div>
        {#if opt.syslog && caps.filter_rules?.length}
          <h3 style="margin-top:14px">Bei welchen Firewall-Regeln soll Logging an?</h3>
          <div class="scroll" style="max-height:230px;overflow-y:auto"><table>
            <thead><tr><th></th><th>Chain</th><th>Aktion</th><th>Regel</th></tr></thead>
            <tbody>{#each caps.filter_rules.filter(r => r.chain && !r.managed) as r (r.id)}
              <tr><td><input type="checkbox" checked={opt.fw_rules?.includes(r.id)} onchange={() => toggleRule(r.id)} disabled={r.disabled} aria-label="Logging für Regel {r.id}" /></td>
                <td>{r.chain}</td><td><span class="badge" class:bad={r.action === 'drop' || r.action === 'reject'}>{r.action}</span></td>
                <td><b>{r.comment || '—'}</b><div class="muted mono" style="font-size:11.5px">{r.summary}{r.disabled ? ' · deaktiviert' : ''}{r.log ? ' · loggt bereits' : ''}</div></td></tr>
            {/each}</tbody></table></div>
          <p class="muted" style="font-size:12.5px;margin:6px 0 0">Vorausgewählt: alle aktiven Drop/Reject-Regeln. Nur <span class="mono">log</span> und <span class="mono">log-prefix</span> werden geändert.</p>
        {/if}
      {/if}
    </div>

    {#if f.mode === 'auto'}
      <div class="box" style="margin-top:14px">
        <h3>Was mtmon auf dem Router ändert <span class="muted">({plan.length} Schritte)</span></h3>
        <p class="muted" style="margin:0 0 10px">Jeder Schritt wird mitprotokolliert und lässt sich per Offboarding zurückbauen. Schlägt irgendein Schritt fehl, baut mtmon automatisch alles wieder zurück.</p>
        {#each plan as s, i (s.key)}
          <div class="step">
            <button class="sh" onclick={() => (open[s.key] = !open[s.key])} aria-expanded={!!open[s.key]}>
              <span class="n">{i + 1}</span><b>{s.title}</b><span class="badge {risk[s.risk]?.[0]}">Risiko: {risk[s.risk]?.[1]}</span><span class="chev">{open[s.key] ? '▾' : '▸'}</span>
            </button>
            <div class="why">{s.why}</div>
            {#if open[s.key]}
              <pre class="code">{s.commands.join('\n')}</pre>
              <div class="muted" style="font-size:12.5px">↩ Rückbau: {s.undo}</div>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
    {#if err}<div class="warnbox" style="margin-top:12px">{err}</div>{/if}

  {:else if step === 'running'}
    <div class="empty"><span class="dot pulse"></span> Richte {opt.name} ein … (Backup, Benutzer, Traffic Flow, Syslog, Test)</div>

  {:else if step === 'done' && result}
    <div class="warnbox" style="background:{ok ? 'var(--ok-bg)' : 'var(--bad-bg)'};border-color:{ok ? 'var(--ok)' : 'var(--bad)'}">
      {#if ok}<b>{opt.name} ist eingerichtet.</b> Der Router sendet jetzt Flows{opt.syslog ? ' und Firewall-Logs' : ''} an mtmon. Die ersten Daten erscheinen nach wenigen Sekunden.
      {:else}<b>Einrichtung fehlgeschlagen.</b> {result.result.error}
        {#if result.result.rolled_back}<br>mtmon hat alle bereits gemachten Änderungen <b>automatisch zurückgebaut</b>.{/if}{/if}
    </div>
    <div class="steps">
      {#each result.result.steps as s}<div class="srow"><span class="badge {s.ok ? 'ok' : 'bad'}">{s.ok ? '✓' : '✗'}</span> {s.title}{#if s.msg}<div class="muted" style="font-size:12.5px">{s.msg}</div>{/if}</div>{/each}
      {#each result.result.rollback || [] as s}<div class="srow"><span class="badge {s.ok ? 'ok' : 'bad'}">↩</span> {s.title}{#if s.msg}<div class="muted" style="font-size:12.5px">{s.msg}</div>{/if}</div>{/each}
    </div>
    {#if result.result.backup}<p class="muted">Konfig-Sicherung auf dem Router: <span class="mono">{result.result.backup}</span> (Files)</p>{/if}
    {#if result.manual_script}<p><b>Rückbau war unvollständig.</b> Diese Befehle im RouterOS-Terminal ausführen:</p><pre class="code">{result.manual_script}</pre>{/if}
  {/if}

  {#snippet footer()}
    {#if step === 'review'}
      <button class="btn" onclick={() => { step = 'connect'; err = '' }}>Zurück</button>
      <button class="btn primary" disabled={busy || !fpOk || !opt.name || (f.mode === 'auto' && !pr.can_write)} onclick={apply}>
        {f.mode === 'auto' ? 'Jetzt einrichten' : 'Hinzufügen'}</button>
    {:else if step === 'done'}
      {#if ok}<button class="btn primary" onclick={() => { onclose(); go('device/' + encodeURIComponent(result.name)) }}>Zum Gerät</button>
      {:else}<button class="btn" onclick={() => { step = 'review'; result = null }}>Zurück zur Prüfung</button>{/if}
      <button class="btn" onclick={onclose}>Schließen</button>
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
