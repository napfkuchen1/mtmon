<script>
  // Application list with share bars and an "active now" dot (client detail + Services page).
  import { bytes, num, ago } from './format.js'
  import { t, tr } from './i18n.svelte.js'
  let { apps = [], unknown = 0, known = 0, truncated = false, empty = null, showActive = true } = $props()
  const tot = a => a.up + a.down
  const max = $derived(apps.reduce((m, a) => Math.max(m, tot(a)), 0) || 1)
  const share = $derived(known + unknown > 0 ? Math.round((known / (known + unknown)) * 100) : 0)
</script>
{#if !apps.length}
  <div class="empty">{empty ?? t('No applications recognised in this range')}
    <div class="faint" style="font-size:12.5px;margin-top:4px">{t('Apps are recognised by DNS names; encrypted DNS (DoH) hides them.')}</div></div>
{:else}
  <div class="list">
    {#each apps as a (a.key)}
      <div class="row">
        <div class="top">
          <div class="name">
            {#if showActive}<span class="dot" class:ok={a.active} class:pulse={a.active} title={a.active ? t('Active now') : t('Not active in the last 2 minutes')} role="img" aria-label={a.active ? t('Active now') : t('Not active in the last 2 minutes')}></span>{/if}
            <b>{tr(a.name)}</b>
            <span class="badge">{tr(a.category)}</span>
            {#if a.active}<span class="badge ok">{t('active now')}</span>{/if}
          </div>
          <div class="val num">{bytes(tot(a))}</div>
        </div>
        <div class="bar"><i style="width:{(tot(a) / max) * 100}%"></i></div>
        <div class="split muted num">
          <span>↓ {bytes(a.down)}</span><span>↑ {bytes(a.up)}</span>
          <span>{a.dests === 1 ? t('1 destination') : t('{n} destinations', { n: num(a.dests) })}</span>
          {#if a.last_seen}<span>{t('last traffic {when}', { when: ago(a.last_seen) })}</span>{/if}
        </div>
      </div>
    {/each}
  </div>
  <div class="foot muted">
    {t('{p}% of the traffic could be attributed to an app; the rest has no known name.', { p: share })}
    {#if truncated} {t('Only the busiest destinations are analysed.')}{/if}
  </div>
{/if}
<style>
  .list { display: flex; flex-direction: column; }
  .row { padding: 10px 16px; border-bottom: 1px solid var(--border); }
  .top { display: flex; justify-content: space-between; gap: 12px; margin-bottom: 6px; }
  .name { display: flex; align-items: center; gap: 8px; min-width: 0; flex-wrap: wrap; }
  .val { font-weight: 600; white-space: nowrap; }
  .split { display: flex; gap: 14px; font-size: 12px; margin-top: 5px; flex-wrap: wrap; }
  .foot { padding: 10px 16px; font-size: 12.5px; }
</style>
