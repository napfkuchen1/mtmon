<script>
  import { api } from '../lib/api.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { datetime, ago } from '../lib/format.js'
  let showInfo = $state(false)
  const d = poll(() => api('/alerts'), 10000)
  const rows = $derived((d.data || []).filter(a => showInfo || a.severity !== 'info'))
  async function ack(a) { await api(`/alerts/${a.id}/ack`, { method: 'POST' }); a.acked = true; d.data = [...d.data] }
  const cls = { critical: 'bad', warning: 'warn', info: '' }
</script>
<div class="head"><h1>Alerts</h1>
  <label class="muted" style="margin-left:auto;display:flex;gap:8px;align-items:center"><input type="checkbox" bind:checked={showInfo} /> show info events</label></div>
<div class="card"><div class="scroll"><table>
  <thead><tr><th>Time</th><th>Severity</th><th>Type</th><th>Message</th><th></th></tr></thead>
  <tbody>
    {#each rows as a (a.id)}
      <tr style="opacity:{a.acked ? .55 : 1}">
        <td class="muted" title={datetime(a.ts)} style="white-space:nowrap">{ago(a.ts)}</td>
        <td><span class="badge {cls[a.severity]}">{a.severity}</span></td>
        <td class="mono">{a.kind}</td><td>{a.msg}</td>
        <td class="r">{#if !a.acked}<button class="btn sm" onclick={() => ack(a)}>Acknowledge</button>{:else}<span class="muted">acked</span>{/if}</td>
      </tr>
    {/each}
  </tbody></table>
  {#if !rows.length}<div class="empty">{d.loading ? 'Loading…' : 'No alerts — all quiet'}</div>{/if}
</div></div>
<style>.head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 18px; }</style>
