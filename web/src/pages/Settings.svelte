<script>
  import { api } from '../lib/api.js'
  import { toast } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { bytes, dur, num } from '../lib/format.js'
  const setup = poll(() => api('/setup'), 0)
  const sys = poll(() => api('/system'), 5000)
  const dev = poll(() => api('/devices'), 0)
  async function copy(t) { try { await navigator.clipboard.writeText(t); toast('Copied') } catch { toast('Copy failed – select the text manually') } }
  const s = $derived(sys.data), u = $derived(setup.data)
  const blocks = $derived(u ? [
    ['1 · Enable Traffic-Flow (IPFIX) on each MikroTik', u.flow],
    ['2 · Create a read-only API user', u.api_user],
    ['3 · Enable REST over HTTPS (www-ssl) restricted to mtmon', u.rest_tls]
  ] : [])
</script>

<div class="head"><h1>Settings & setup</h1></div>

<div class="grid g2">
  <div class="card"><div class="card-h"><h2>System</h2></div>
    <table><tbody>
      {#if s}
        <tr><td class="muted">Version</td><td class="r">{s.version}</td></tr>
        <tr><td class="muted">Uptime</td><td class="r">{dur(s.uptime_s)}</td></tr>
        <tr><td class="muted">Flow packets / records</td><td class="r num">{num(s.flow_packets)} / {num(s.flow_records)}</td></tr>
        <tr><td class="muted">Flow rate</td><td class="r num">{s.flows_ps.toFixed(0)} / s</td></tr>
        <tr><td class="muted">Decode errors · packets without template</td><td class="r num">{s.flow_errors} · {s.flow_no_template}</td></tr>
        <tr><td class="muted">Dropped (unknown exporter)</td><td class="r num">{s.flow_dropped_unknown_exporter}</td></tr>
        <tr><td class="muted">Ignored transit flows (WAN-side copies)</td><td class="r num">{num(s.ignored_transit)}</td></tr>
        <tr><td class="muted">Write queue · rows dropped</td><td class="r num">{s.queue} · {s.db_dropped}</td></tr>
        <tr><td class="muted">Database size</td><td class="r num">{bytes(s.db_bytes)}</td></tr>
        <tr><td class="muted">Retention (raw / rollup)</td><td class="r">{s.raw_retention_days} d / {s.retention_days} d</td></tr>
      {/if}
    </tbody></table></div>
  <div class="card"><div class="card-h"><h2>Enrichment</h2></div>
    <table><tbody>
      {#if s}
        {#each [['geo', 'GeoIP country database'], ['asn', 'ASN database'], ['oui', 'MAC vendor (OUI) list'], ['reverse_dns', 'Reverse DNS fallback']] as [k, l]}
          <tr><td>{l}</td><td class="r"><span class="badge {s.enrich[k] ? 'ok' : ''}">{s.enrich[k] ? 'active' : 'not configured'}</span></td></tr>
        {/each}
      {/if}
    </tbody></table>
    <div class="card-b muted" style="border-top:1px solid var(--border)">Router DNS caches are always used as the primary IP→domain source. GeoIP/ASN need offline <span class="mono">.mmdb</span> files: run <span class="mono">mtmon-update-geo</span> in the container (see <span class="mono">docs/runbook.md</span>).
      {#if s?.enrich.geo || s?.enrich.asn}<div style="margin-top:6px">IP geolocation data by <a href="https://db-ip.com" target="_blank" rel="noopener noreferrer">DB-IP</a> (CC BY 4.0).</div>{/if}</div></div>
</div>

<div class="card" style="margin-top:16px"><div class="card-h"><h2>Router setup</h2><span class="muted">mtmon address detected: <span class="mono">{u?.mtmon_ip}</span></span></div>
  <div class="card-b" style="display:flex;flex-direction:column;gap:16px">
    {#each blocks as [title, code]}
      <div><div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px"><b>{title}</b><button class="btn sm" onclick={() => copy(code)}>Copy</button></div><pre class="code">{code}</pre></div>
    {/each}
    {#if u}
      <div class="warnbox"><b>FastTrack:</b> {u.fasttrack}</div>
      <div class="muted">Pin the router certificate: <span class="mono">{u.fingerprint}</span>. Then test everything with <span class="mono">mtmon check</span>.</div>
    {/if}
  </div></div>

<div class="card" style="margin-top:16px"><div class="card-h"><h2>Configured devices</h2><span class="muted">edit <span class="mono">/etc/mtmon/config.json</span>, then <span class="mono">systemctl restart mtmon</span></span></div>
  <table><thead><tr><th>Name</th><th>Address</th><th>Role</th><th>Site</th></tr></thead><tbody>
    {#each dev.data || [] as x}<tr><td><b>{x.name}</b></td><td class="mono">{x.addr}</td><td>{x.role}</td><td class="muted">{x.site || '—'}</td></tr>{/each}
  </tbody></table></div>
<style>.head { margin-bottom: 18px; }</style>
