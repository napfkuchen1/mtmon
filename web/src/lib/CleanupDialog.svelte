<script>
  import { api, download } from './api.js'
  import { t, tr } from './i18n.svelte.js'
  import { toast } from './state.svelte.js'
  import { ago, num } from './format.js'
  import Modal from './Modal.svelte'
  let { onclose, ondone } = $props()
  let days = $state(90), keep = $state(true), unused = $state(false), prev = $state(null), err = $state(''), busy = $state(false), loading = $state(false)
  const choices = [0, 1, 7, 30, 90, 180, 365]
  const dayLabel = n => (n === 0 ? t('Offline right now') : t('{n} days', { n }))

  // live dry-run preview whenever the settings change
  $effect(() => {
    const body = { days, unused_days: unused ? 7 : 0, keep_labeled: keep, dry_run: true }
    let stop = false
    loading = true
    const h = setTimeout(async () => {
      try { const r = await api('/clients/cleanup', { method: 'POST', body }); if (!stop) { prev = r; err = '' } }
      catch (e) { if (!stop) { prev = null; err = e.message } }
      finally { if (!stop) loading = false }
    }, 150)
    return () => { stop = true; clearTimeout(h) }
  })
  async function run() {
    busy = true
    try {
      const r = await api('/clients/cleanup', { method: 'POST', body: { days, unused_days: unused ? 7 : 0, keep_labeled: keep, dry_run: false } })
      toast(t('{n} stale clients removed', { n: num(r.removed) }))
      ondone?.(r); onclose()
    } catch (e) { err = e.message } finally { busy = false }
  }
</script>
<Modal title={t('Clean up stale clients')} {onclose}>
  <p class="muted" style="margin-top:0">{t('Removes clients that are offline, together with their address and roaming history. Traffic statistics stay. Online clients and your routers/APs are never removed. A removed device that returns is simply listed again.')}</p>
  <div class="form">
    <label>{t('Offline for at least')}
      <select class="input" bind:value={days}>{#each choices as d}<option value={d}>{dayLabel(d)}</option>{/each}</select></label>
    <label class="chk"><input type="checkbox" bind:checked={unused} /> {t('Only devices without any traffic in the last 7 days')}</label>
    <label class="chk"><input type="checkbox" bind:checked={keep} /> {t('Keep devices that have a label')}</label>
  </div>
  <div class="prev" aria-live="polite">
    {#if prev}
      <div><b>{t('{n} clients would be removed', { n: num(prev.count) })}</b>{#if loading} <span class="muted">…</span>{/if}</div>
      {#if prev.sample.length}
        <table><tbody>
          {#each prev.sample as c (c.mac)}
            <tr><td>{c.name}<div class="muted mono" style="font-size:11.5px">{c.mac}</div></td><td class="r muted">{ago(c.last_seen)}</td></tr>
          {/each}
        </tbody></table>
        {#if prev.count > prev.sample.length}<div class="muted" style="font-size:12.5px;padding-top:6px">{t('… and {n} more', { n: num(prev.count - prev.sample.length) })}</div>{/if}
      {/if}
    {:else if !err}<span class="muted">{t('Loading…')}</span>{/if}
  </div>
  {#if err}<div class="warnbox" style="margin-top:12px">{tr(err)}</div>{/if}
  {#snippet footer()}
    <button class="btn" style="margin-right:auto" title={t('Safety copy of all clients (name, MAC, IP, times) before you remove anything')} onclick={() => download('/export/clients?format=xlsx')}>{t('Download list (Excel)')}</button>
    <button class="btn" onclick={() => download('/export/clients')}>{t('Download list (CSV)')}</button>
    <button class="btn" onclick={onclose}>{t('Cancel')}</button>
    <button class="btn primary" disabled={busy || loading || !prev?.count} onclick={run}>{prev?.count ? t('Remove {n} clients', { n: num(prev.count) }) : t('Nothing to remove')}</button>
  {/snippet}
</Modal>
<style>
  .form { display: grid; gap: 12px; } label { display: grid; gap: 4px; font-weight: 500; font-size: 13px; }
  .chk { display: flex; gap: 8px; align-items: center; }
  .prev { margin-top: 14px; padding: 10px 12px; background: var(--card-2); border: 1px solid var(--border); border-radius: 8px; font-size: 13px; }
  .prev table { width: 100%; margin-top: 6px; } .prev td { padding: 5px 4px; } .r { text-align: right; }
</style>
