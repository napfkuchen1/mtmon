<script>
  import { api } from '../lib/api.js'
  import { t, tr } from '../lib/i18n.svelte.js'
  import { app, toast, setPref } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { bytes, dur, num } from '../lib/format.js'
  import UpdateCard from './UpdateCard.svelte'
  const setup = poll(() => api('/setup'), 0)
  const sys = poll(() => api('/system'), 5000)
  const dev = poll(() => api('/devices'), 0)
  async function copy(text) { try { await navigator.clipboard.writeText(text); toast(t('Copied')) } catch { toast(t('Copy failed – select the text manually')) } }
  const s = $derived(sys.data), u = $derived(setup.data)
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
  const blocks = $derived(u ? [
    [t('1 · Enable Traffic-Flow (IPFIX) on each MikroTik'), u.flow],
    [t('2 · Create a read-only API user'), u.api_user],
    [t('3 · Enable REST over HTTPS (www-ssl) restricted to mtmon'), u.rest_tls]
  ] : [])
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

<div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('Appearance')}</h2></div>
  <table><tbody>
    <tr><td>{t('Density')}<div class="muted" style="font-size:12px">{t('Compact fits more rows on the screen')}</div></td><td class="r"><div class="tabs">
      {#each [['comfortable', t('Comfortable')], ['compact', t('Compact')]] as [id, l]}<button class:on={app.density === id} onclick={() => setPref('density', id)}>{l}</button>{/each}</div></td></tr>
    <tr><td>{t('Chart style')}<div class="muted" style="font-size:12px">{t('Modern: smooth curves, peak values, night shading. Classic: the previous straight-line look.')}</div></td><td class="r"><div class="tabs">
      {#each [['modern', t('Modern')], ['classic', t('Classic')]] as [id, l]}<button class:on={app.chart === id} onclick={() => setPref('chart', id)}>{l}</button>{/each}</div></td></tr>
  </tbody></table></div>

<UpdateCard />

<div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('Router setup')}</h2><span class="muted">{t('mtmon address detected:')} <span class="mono">{u?.mtmon_ip}</span></span></div>
  <div class="card-b" style="display:flex;flex-direction:column;gap:16px">
    {#each blocks as [title, code]}
      <div><div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px"><b>{title}</b><button class="btn sm" onclick={() => copy(code)}>{t('Copy')}</button></div><pre class="code">{code}</pre></div>
    {/each}
    {#if u}
      <div class="warnbox"><b>FastTrack:</b> {tr(u.fasttrack)}</div>
      <div class="muted">{t('Pin the router certificate:')} <span class="mono">{u.fingerprint}</span>. {t('Then test everything with')} <span class="mono">mtmon check</span>.</div>
    {/if}
  </div></div>

<div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('Configured devices')}</h2><span class="muted">{t('edit')} <span class="mono">/etc/mtmon/config.json</span>, {t('then')} <span class="mono">systemctl restart mtmon</span></span></div>
  <table><thead><tr><th>{t('Name')}</th><th>{t('Address')}</th><th>{t('Role')}</th><th>{t('Site')}</th></tr></thead><tbody>
    {#each dev.data || [] as x}<tr><td><b>{x.name}</b></td><td class="mono">{x.addr}</td><td>{x.role}</td><td class="muted">{x.site || '—'}</td></tr>{/each}
  </tbody></table></div>
<style>
  .head { margin-bottom: 18px; }
  .steps { display: flex; flex-direction: column; gap: 4px; }
  .stp { display: flex; gap: 8px; align-items: baseline; font-size: 13px; }
  .stp .ic { width: 16px; text-align: center; font-weight: 700; color: var(--muted); }
  .stp.ok .ic { color: var(--ok); } .stp.failed .ic, .bad-t { color: var(--bad); }
</style>
