<script>
  import Flag from './Flag.svelte'
  import Empty from './Empty.svelte'
  import { bytes, num, countryName } from './format.js'
  import { t, tr, i18n } from './i18n.svelte.js'
  let { rows = [], onpick = null, showFlows = false, mode = 'traffic', empty = null, emptyIcon = 'generic', emptyHref = '', emptyAction = '' } = $props()
  const total = r => r.up + r.down + r.internal
  const max = $derived(rows.reduce((m, r) => Math.max(m, total(r)), 0) || 1)
</script>

{#if !rows.length}
  <Empty compact icon={emptyIcon} title={empty ?? t('No traffic recorded in this range')} href={emptyHref} action={emptyAction} />
{:else}
  <div class="list">
    {#each rows as r (r.key + '|' + r.sub)}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <div class="row" class:click={!!onpick} role={onpick ? 'button' : undefined} tabindex={onpick ? 0 : undefined}
           onclick={() => onpick?.(r)} onkeydown={e => e.key === 'Enter' && onpick?.(r)}>
        <div class="top">
          <div class="name" title={tr(r.label)}>{#if mode === 'country' && r.key}<Flag cc={r.key} />{countryName(r.key, i18n.lang)} <span class="sub">{r.key}</span>{:else}{tr(r.label || r.key)}{/if}
            {#if r.sub && r.sub !== r.label}<span class="sub">{tr(r.sub)}</span>{/if}</div>
          <div class="val num">{bytes(total(r))}</div>
        </div>
        <div class="bar"><i style="width:{(total(r) / max) * 100}%"></i></div>
        <div class="split muted num">
          <span>↓ {bytes(r.down)}</span><span>↑ {bytes(r.up)}</span>
          {#if r.internal}<span>⇄ {bytes(r.internal)}</span>{/if}
          {#if showFlows}<span>{t('{n} flows', { n: num(r.flows) })}</span>{/if}
        </div>
      </div>
    {/each}
  </div>
{/if}

<style>
  .list { display: flex; flex-direction: column; }
  .row { padding: 10px 16px; border-bottom: 1px solid var(--border); }
  .row:last-child { border-bottom: 0; }
  .row.click { cursor: pointer; } .row.click:hover { background: var(--card-2); }
  .top { display: flex; justify-content: space-between; gap: 12px; margin-bottom: 6px; }
  .name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
  .sub { color: var(--muted); font-weight: 400; margin-left: 8px; font-size: 12.5px; }
  .val { font-weight: 600; white-space: nowrap; }
  .split { display: flex; gap: 14px; font-size: 12px; margin-top: 5px; }
</style>
