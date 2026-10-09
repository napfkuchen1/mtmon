<script>
  import Empty from '../lib/Empty.svelte'
  import { api, download } from '../lib/api.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { datetime, ago } from '../lib/format.js'
  import { t, tr } from '../lib/i18n.svelte.js'
  let showInfo = $state(false)
  const d = poll(() => api('/alerts'), 10000)
  const rows = $derived((d.data || []).filter(a => showInfo || a.severity !== 'info'))
  async function ack(a) { await api(`/alerts/${a.id}/ack`, { method: 'POST' }); a.acked = true; d.data = [...d.data] }
  const open = $derived((d.data || []).filter(a => !a.acked).length)
  const done = $derived((d.data || []).filter(a => a.acked).length)
  async function ackAll() { await api('/alerts/ack-all', { method: 'POST' }); d.data = (d.data || []).map(a => ({ ...a, acked: true })) }
  async function clear() {
    if (!confirm(t('Remove all acknowledged alerts from the list? This cannot be undone.'))) return
    await api('/alerts/clear', { method: 'POST' }); d.data = (d.data || []).filter(a => !a.acked)
  }
  const cls = { critical: 'bad', warning: 'warn', info: '' }
</script>
<div class="head"><h1>{t('Alerts')}</h1>
  <label class="muted" style="margin-left:auto;display:flex;gap:8px;align-items:center"><input type="checkbox" bind:checked={showInfo} /> {t('show info events')}</label>
  <button class="btn sm" disabled={!open} onclick={ackAll}>{t('Acknowledge all')}</button>
  <button class="btn sm" disabled={!done} onclick={clear}>{t('Clear alerts')}</button>
  <button class="btn sm" onclick={() => download('/export/alerts?format=xlsx')}>{t('Export Excel')}</button></div>
<div class="card"><div class="scroll"><table>
  <thead><tr><th>{t('Time')}</th><th>{t('Severity')}</th><th>{t('Type')}</th><th>{t('Message')}</th><th></th></tr></thead>
  <tbody>
    {#each rows as a (a.id)}
      <tr style="opacity:{a.acked ? .55 : 1}">
        <td class="muted" title={ago(a.ts)} style="white-space:nowrap">{datetime(a.ts)}</td>
        <td><span class="badge {cls[a.severity]}">{tr(a.severity)}</span></td>
        <td class="mono">{tr(a.kind)}</td><td>{tr(a.msg)}</td>
        <td class="r">{#if !a.acked}<button class="btn sm" onclick={() => ack(a)}>{t('Acknowledge')}</button>{:else}<span class="muted">{t('acked')}</span>{/if}</td>
      </tr>
    {/each}
  </tbody></table>
  {#if !rows.length}{#if d.loading}<div class="empty">{t('Loading…')}</div>{:else}<Empty icon="bulb" title={t('No alerts — all quiet')} hint={t('mtmon tells you here when a device goes down, a port flaps or something unusual happens.')} />{/if}{/if}
</div></div>
<style>.head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 18px; }</style>
