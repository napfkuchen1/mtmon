<script>
  import { api } from './api.js'
  import { t, tr } from './i18n.svelte.js'
  import { toast } from './state.svelte.js'
  import { ago, num } from './format.js'
  import Modal from './Modal.svelte'
  let { onclose, ondone } = $props()
  let days = $state(90), keep = $state(true), prev = $state(null), err = $state(''), busy = $state(false), loading = $state(false)
  const choices = [30, 90, 180, 365]

  // live dry-run preview whenever the settings change
  $effect(() => {
    const body = { days, keep_labeled: keep, dry_run: true }
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
      const r = await api('/clients/cleanup', { method: 'POST', body: { days, keep_labeled: keep, dry_run: false } })
      toast(t('{n} stale clients removed', { n: num(r.removed) }))
      ondone?.(r); onclose()
    } catch (e) { err = e.message } finally { busy = false }
  }
</script>
<Modal title={t('Clean up stale clients')} {onclose}>
  <p class="muted" style="margin-top:0">{t('Removes clients that have not been seen for a long time, together with their address and roaming history. Traffic statistics stay. Online clients and your routers/APs are never removed.')}</p>
  <div class="form">
    <label>{t('Not seen for at least')}
      <select class="input" bind:value={days}>{#each choices as d}<option value={d}>{t('{n} days', { n: d })}</option>{/each}</select></label>
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
