<script>
  import { bytes, num, flag } from './format.js'
  let { rows = [], onpick = null, showFlows = false, mode = 'traffic', empty = 'No traffic recorded in this range' } = $props()
  const total = r => r.up + r.down + r.internal
  const max = $derived(rows.reduce((m, r) => Math.max(m, total(r)), 0) || 1)
</script>

{#if !rows.length}
  <div class="empty">{empty}</div>
{:else}
  <div class="list">
    {#each rows as r (r.key + '|' + r.sub)}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <div class="row" class:click={!!onpick} role={onpick ? 'button' : undefined} tabindex={onpick ? 0 : undefined}
           onclick={() => onpick?.(r)} onkeydown={e => e.key === 'Enter' && onpick?.(r)}>
        <div class="top">
          <div class="name" title={r.label}>{#if mode === 'country' && r.key}{flag(r.key)} {/if}{r.label || r.key}
            {#if r.sub && r.sub !== r.label}<span class="sub">{r.sub}</span>{/if}</div>
          <div class="val num">{bytes(total(r))}</div>
        </div>
        <div class="bar"><i style="width:{(total(r) / max) * 100}%"></i></div>
        <div class="split muted num">
          <span>↓ {bytes(r.down)}</span><span>↑ {bytes(r.up)}</span>
          {#if r.internal}<span>⇄ {bytes(r.internal)}</span>{/if}
          {#if showFlows}<span>{num(r.flows)} flows</span>{/if}
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
