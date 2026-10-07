<script>
  import { api } from '../lib/api.js'
  import { t, tr } from '../lib/i18n.svelte.js'
  import { app, toast, setPref, ACCENTS } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { bytes, dur, num, ago, datetime } from '../lib/format.js'
  import UpdateCard from './UpdateCard.svelte'
  const sys = poll(() => api('/system'), 5000)
  const dev = poll(() => api('/devices'), 0)
  const s = $derived(sys.data)
  // --- access: port, public URL (reverse proxy), nginx snippet, restart
  let acc = $state(null), accPort = $state(''), accUrl = $state(''), accBusy = $state(false), accErr = $state(''), restarting = $state(false)
  async function loadAcc() { try { acc = await api('/access'); accPort = acc.port_override || ''; accUrl = acc.public_url || '' } catch (e) { accErr = e.message } }
  loadAcc()
  async function saveAcc() {
    accBusy = true; accErr = ''
    try { acc = await api('/access', { method: 'PUT', body: { public_url: accUrl, listen_port: accPort ? Number(accPort) : 0 } }); toast(t('Saved')) }
    catch (e) { accErr = e.message } finally { accBusy = false }
  }
  async function restart() {
    if (!confirm(t('Restart mtmon now? The page reconnects automatically after a few seconds.'))) return
    restarting = true
    try { await api('/system/restart', { method: 'POST', body: {} }) } catch {}
    // wait until the service answers again, then reload (the port may have changed: stay on this origin if it works)
    const t0 = Date.now()
    const h = setInterval(async () => {
      try { const r = await fetch('/api/me', { cache: 'no-store' }); if (r.ok) { clearInterval(h); location.reload() } } catch {}
      if (Date.now() - t0 > 60000) { clearInterval(h); restarting = false; toast(t('mtmon did not come back on this address – if you changed the port, open the new port')) }
    }, 2000)
  }
  const portPending = $derived(acc && acc.port_override && acc.port_override !== acc.port)
  const host = $derived((accUrl && (() => { try { return new URL(accUrl).host } catch { return '' } })()) || 'mtmon.example.com')
  const nginx = $derived(acc ? `server {
    listen 443 ssl;
    server_name ${host.split(':')[0]};
    # ssl_certificate / ssl_certificate_key ...

    location / {
        proxy_pass ${acc.tls ? 'https' : 'http'}://127.0.0.1:${acc.port};${acc.tls ? '\n        proxy_ssl_verify off;   # mtmon uses its own self-signed certificate' : ''}
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;       # live view (WebSocket)
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 1h;
    }
}` : '')
  async function copyText(x) { try { await navigator.clipboard.writeText(x); toast(t('Copied')) } catch { toast(t('Copy failed – select the text manually')) } }

  // --- API tokens (read-only)
  let tokens = $state([]), tokName = $state(''), newTok = $state(''), tokErr = $state('')
  async function loadTokens() { try { tokens = await api('/tokens') } catch (e) { tokErr = e.message } }
  loadTokens()
  async function createToken() {
    tokErr = ''; newTok = ''
    try { const r = await api('/tokens', { method: 'POST', body: { name: tokName } }); newTok = r.token; tokName = ''; loadTokens() } catch (e) { tokErr = e.message }
  }
  async function revoke(tk) {
    if (!confirm(t('Revoke the token “{name}”? Everything using it stops working.', { name: tk.name }))) return
    try { await api('/tokens/' + tk.id, { method: 'DELETE' }); loadTokens() } catch (e) { tokErr = e.message }
  }

  // --- enrichment: one-click download of GeoIP / ASN / vendor data, runtime switch for reverse DNS
  let job = $state(null), rdnsBusy = $state(false), jobErr = $state('')
  const names = { geo: t('GeoIP country database'), asn: t('ASN database'), oui: t('MAC vendor (OUI) list') }
  const loadJob = async () => { try { job = await api('/enrich/setup') } catch {} }
  loadJob()
  $effect(() => {
    if (!job?.running) return
    const h = setInterval(async () => { await loadJob(); if (!job?.running) { try { sys.data = await api('/system') } catch {} } }, 1500)
    return () => clearInterval(h)
  })
  async function startSetup() {
    jobErr = ''
    try { job = await api('/enrich/setup', { method: 'POST', body: {} }) } catch (e) { jobErr = e.message }
  }
  async function toggleRdns() {
    rdnsBusy = true
    try {
      const r = await api('/enrich/reverse_dns', { method: 'POST', body: { enabled: !s.enrich.reverse_dns } })
      sys.data = await api('/system')
      toast(r.persisted ? t('Saved') : t('Active now – could not be saved to the config file'))
    } catch (e) { toast(tr(e.message)) } finally { rdnsBusy = false }
  }
  const missing = $derived(s ? ['geo', 'asn', 'oui'].filter(k => !s.enrich[k]) : [])
  const stIcon = { pending: '·', running: '…', ok: '✓', failed: '✗' }

</script>

<div class="head"><h1>{t('Settings & setup')}</h1></div>

<div class="grid g2">
  <div class="card"><div class="card-h"><h2>{t('System')}</h2></div>
    <table><tbody>
      {#if s}
        <tr><td class="muted">{t('Version')}</td><td class="r">{s.version}</td></tr>
        <tr><td class="muted">{t('Uptime')}</td><td class="r">{dur(s.uptime_s)}</td></tr>
        <tr><td class="muted">{t('Flow packets / records')}</td><td class="r num">{num(s.flow_packets)} / {num(s.flow_records)}</td></tr>
        <tr><td class="muted">{t('Flow rate')}</td><td class="r num">{s.flows_ps.toFixed(0)} / s</td></tr>
        <tr><td class="muted">{t('Decode errors · packets without template')}</td><td class="r num">{s.flow_errors} · {s.flow_no_template}</td></tr>
        <tr><td class="muted">{t('Dropped (unknown exporter)')}</td><td class="r num">{s.flow_dropped_unknown_exporter}</td></tr>
        <tr><td class="muted">{t('Ignored transit flows (WAN-side copies)')}</td><td class="r num">{num(s.ignored_transit)}</td></tr>
        <tr><td class="muted">{t('Write queue · rows dropped')}</td><td class="r num">{s.queue} · {s.db_dropped}</td></tr>
        <tr><td class="muted">{t('Database size')}</td><td class="r num">{bytes(s.db_bytes)}</td></tr>
        <tr><td class="muted">{t('Retention (raw / rollup)')}</td><td class="r">{s.raw_retention_days} d / {s.retention_days} d</td></tr>
      {/if}
    </tbody></table></div>
  <div class="card"><div class="card-h"><h2>{t('Enrichment')}</h2></div>
    <table><tbody>
      {#if s}
        {#each [['geo', t('GeoIP country database'), t('Country and flag for every destination')], ['asn', t('ASN database'), t('Provider name (e.g. Netflix, Google) for an address')], ['oui', t('MAC vendor (OUI) list'), t('Manufacturer of a device from its MAC address')]] as [k, l, hint]}
          <tr><td>{l}<div class="muted" style="font-size:12px">{hint}</div></td><td class="r"><span class="badge {s.enrich[k] ? 'ok' : ''}">{s.enrich[k] ? t('active') : t('not configured')}</span></td></tr>
        {/each}
        <tr><td>{t('Reverse DNS fallback')}<div class="muted" style="font-size:12px">{t('Looks up names for addresses the routers do not know. Sends lookups to your DNS resolver.')}</div></td>
          <td class="r"><button class="sw" class:on={s.enrich.reverse_dns} role="switch" aria-checked={s.enrich.reverse_dns} aria-label={t('Reverse DNS fallback')} disabled={rdnsBusy} onclick={toggleRdns}><i></i></button></td></tr>
      {/if}
    </tbody></table>
    <div class="card-b" style="border-top:1px solid var(--border);display:flex;flex-direction:column;gap:10px">
      {#if job?.steps?.length}
        <div class="steps">
          {#each job.steps as st}
            <div class="stp {st.status}"><span class="ic">{stIcon[st.status]}</span><span>{names[st.what]}</span>
              {#if st.status === 'running'}<span class="muted">{t('downloading…')}</span>{/if}
              {#if st.status === 'failed'}<span class="bad-t">{tr(st.error)}</span>{/if}</div>
          {/each}
        </div>
      {/if}
      {#if jobErr}<div class="warnbox">{tr(jobErr)}</div>{/if}
      <div style="display:flex;gap:10px;align-items:center;flex-wrap:wrap">
        <button class="btn" class:primary={missing.length > 0} disabled={job?.running} onclick={startSetup}>
          {job?.running ? t('Downloading…') : missing.length ? t('Set up now (about 20 MB)') : t('Update databases')}</button>
        <span class="muted" style="font-size:12.5px">{t('mtmon downloads the free DB-IP and IEEE lists itself and uses them right away. Needs internet access from the container.')}</span>
      </div>
      {#if job?.steps?.some(x => x.status === 'failed')}
        <div class="muted" style="font-size:12.5px">{t('If the download keeps failing (no internet from the container?), run {c} in the container, or copy the files to {d} (see {r}).', { c: 'mtmon-update-geo', d: '/var/lib/mtmon/geo/', r: 'docs/runbook.md' })}</div>
      {/if}
      <div class="muted" style="font-size:12.5px">{t('Router DNS caches are always used as the primary IP→domain source.')}
        {#if s?.enrich.geo || s?.enrich.asn}{t('IP geolocation data by')} <a href="https://db-ip.com" target="_blank" rel="noopener noreferrer">DB-IP</a> (CC BY 4.0).{/if}</div>
    </div></div>
</div>

<div class="grid g2" style="margin-top:16px">
  <div class="card"><div class="card-h"><h2>{t('Access & reverse proxy')}</h2></div>
    <div class="card-b" style="display:flex;flex-direction:column;gap:12px">
      {#if acc}
        <div class="muted" style="font-size:13px">{t('mtmon listens on')} <span class="mono">{acc.listen}</span> ({acc.tls ? 'HTTPS' : 'HTTP'}).</div>
        <label class="fld">{t('Port')}
          <div style="display:flex;gap:8px;flex-wrap:wrap"><input class="input" style="width:120px" type="number" min="1024" max="65535" placeholder={acc.port} bind:value={accPort} />
            {#if portPending}<span class="badge warn">{t('applies after restart')}</span>{/if}</div>
          <span class="muted" style="font-size:12px">{t('1024–65535. Empty = as configured. If the port cannot be used mtmon keeps the old one, so you cannot lock yourself out.')}</span></label>
        <label class="fld">{t('Public URL (when mtmon is behind a reverse proxy)')}
          <input class="input" placeholder="https://mtmon.example.com" bind:value={accUrl} />
          <span class="muted" style="font-size:12px">{t('Browsers send this address as “Origin”. mtmon accepts it for logins, changes and the live view.')}</span></label>
        {#if accErr}<div class="warnbox">{tr(accErr)}</div>{/if}
        <div style="display:flex;gap:8px;flex-wrap:wrap">
          <button class="btn primary" disabled={accBusy} onclick={saveAcc}>{t('Save')}</button>
          {#if acc.can_restart}<button class="btn" disabled={restarting} onclick={restart}>{restarting ? t('Restarting …') : t('Restart mtmon')}</button>{/if}
        </div>
        <div class="chk-box">
          <b>{t('Connection check (this browser request)')}</b>
          <table><tbody>
            <tr><td class="muted">Host</td><td class="r mono">{acc.request.host}</td></tr>
            <tr><td class="muted">Origin</td><td class="r mono">{acc.request.origin || t('not sent (normal for a plain page load)')}</td></tr>
            <tr><td class="muted">{t('Address in this browser')}</td><td class="r mono">{location.origin}</td></tr>
            <tr><td class="muted">X-Forwarded-Host / -Proto</td><td class="r mono">{acc.request.forwarded_host || '—'} / {acc.request.forwarded_proto || '—'}</td></tr>
            <tr><td class="muted">{t('Through a proxy')}</td><td class="r">{acc.request.via_proxy ? t('yes') : t('no (direct)')}</td></tr>
            <tr><td class="muted">{t('Changes and live view allowed')}</td><td class="r"><span class="badge {acc.request.origin_ok ? 'ok' : 'bad'}">{acc.request.origin_ok ? t('yes') : t('no')}</span></td></tr>
          </tbody></table>
          {#if acc.request.via_proxy && !acc.request.origin_ok}<div class="warnbox" style="margin-top:8px">{t('The proxy does not pass the original host name. Add “proxy_set_header Host $host;” (see the nginx example) or enter the public URL above.')}</div>{/if}
        </div>
        <details><summary>{t('nginx example')}</summary>
          <div style="display:flex;justify-content:flex-end;margin:6px 0"><button class="btn sm" onclick={() => copyText(nginx)}>{t('Copy')}</button></div>
          <pre class="code">{nginx}</pre>
          <div class="muted" style="font-size:12px;margin-top:6px">{t('Open this page through the proxy and check the connection check above: “Changes and live view allowed” must say yes.')}</div></details>
      {/if}
    </div></div>

  <div class="card"><div class="card-h"><h2>{t('API tokens')}</h2></div>
    <div class="card-b" style="display:flex;flex-direction:column;gap:12px">
      <div class="muted" style="font-size:13px">{t('Read-only access for scripts and other tools (for example Home Assistant). A token can read data but never change anything, and it is shown only once.')}</div>
      <div style="display:flex;gap:8px;flex-wrap:wrap"><input class="input" style="flex:1;min-width:160px" maxlength="40" placeholder={t('Name, e.g. Home Assistant')} bind:value={tokName} onkeydown={e => e.key === 'Enter' && tokName && createToken()} />
        <button class="btn primary" disabled={!tokName.trim()} onclick={createToken}>{t('Create token')}</button></div>
      {#if tokErr}<div class="warnbox">{tr(tokErr)}</div>{/if}
      {#if newTok}
        <div class="warnbox" style="background:var(--ok-bg)"><b>{t('Copy the token now – it is not shown again:')}</b>
          <div style="display:flex;gap:8px;align-items:center;margin-top:6px;flex-wrap:wrap"><code class="mono" style="word-break:break-all">{newTok}</code><button class="btn sm" onclick={() => copyText(newTok)}>{t('Copy')}</button></div>
          <div class="muted" style="font-size:12px;margin-top:6px">curl -H "Authorization: Bearer {newTok.slice(0, 8)}…" {location.origin}/api/overview</div></div>
      {/if}
      {#if tokens.length}
        <table><tbody>
          {#each tokens as tk (tk.id)}
            <tr><td><b>{tk.name}</b><div class="muted mono" style="font-size:11.5px">{tk.prefix}…</div></td>
              <td class="muted" style="font-size:12.5px">{t('created')} {datetime(tk.created)}<br>{tk.last_used ? t('last used {t}', { t: ago(tk.last_used) }) : t('never used')}</td>
              <td class="r"><button class="btn sm" onclick={() => revoke(tk)}>{t('Revoke')}</button></td></tr>
          {/each}
        </tbody></table>
      {:else}<div class="muted" style="font-size:13px">{t('No tokens yet.')}</div>{/if}
    </div></div>
</div>

<div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('Appearance')}</h2></div>
  <table><tbody>
    <tr><td>{t('Density')}<div class="muted" style="font-size:12px">{t('Compact fits more rows on the screen')}</div></td><td class="r"><div class="tabs">
      {#each [['comfortable', t('Comfortable')], ['compact', t('Compact')]] as [id, l]}<button class:on={app.density === id} onclick={() => setPref('density', id)}>{l}</button>{/each}</div></td></tr>
    <tr><td>{t('Colour style')}<div class="muted" style="font-size:12px">{t('Accent colour of buttons, charts and highlights')}</div></td><td class="r"><div class="swatches">
      {#each ACCENTS as [id, l, c]}<button class="swatch" class:on={app.accent === id} style="background:{c}" title={t(l)} aria-label={t(l)} aria-pressed={app.accent === id} onclick={() => setPref('accent', id)}></button>{/each}</div></td></tr>
    <tr><td>{t('Chart style')}<div class="muted" style="font-size:12px">{t('Modern: smooth curves, peak values, night shading. Classic: the previous straight-line look.')}</div></td><td class="r"><div class="tabs">
      {#each [['modern', t('Modern')], ['classic', t('Classic')]] as [id, l]}<button class:on={app.chart === id} onclick={() => setPref('chart', id)}>{l}</button>{/each}</div></td></tr>
  </tbody></table></div>

<UpdateCard />

<div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('Configured devices')}</h2><span class="muted">{t('edit')} <span class="mono">/etc/mtmon/config.json</span>, {t('then')} <span class="mono">systemctl restart mtmon</span></span></div>
  <table><thead><tr><th>{t('Name')}</th><th>{t('Address')}</th><th>{t('Role')}</th><th>{t('Site')}</th></tr></thead><tbody>
    {#each dev.data || [] as x}<tr><td><b>{x.name}</b></td><td class="mono">{x.addr}</td><td>{x.role}</td><td class="muted">{x.site || '—'}</td></tr>{/each}
  </tbody></table></div>
<style>
  .head { margin-bottom: 18px; }
  .fld { display: grid; gap: 4px; font-weight: 500; font-size: 13px; }
  .chk-box { border: 1px solid var(--border); border-radius: 8px; padding: 10px 12px; background: var(--card-2); font-size: 13px; }
  .chk-box table td { padding: 4px 0; border: 0; }
  .swatches { display: inline-flex; flex-wrap: wrap; gap: 8px; justify-content: flex-end; }
  .swatch { width: 24px; height: 24px; border-radius: 50%; border: 2px solid var(--card); box-shadow: 0 0 0 1px var(--border-2); cursor: pointer; padding: 0; }
  .swatch.on { box-shadow: 0 0 0 2px var(--text); }
  .steps { display: flex; flex-direction: column; gap: 4px; }
  .stp { display: flex; gap: 8px; align-items: baseline; font-size: 13px; }
  .stp .ic { width: 16px; text-align: center; font-weight: 700; color: var(--muted); }
  .stp.ok .ic { color: var(--ok); } .stp.failed .ic, .bad-t { color: var(--bad); }
</style>
