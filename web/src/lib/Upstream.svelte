<script>
  // The ISP modem / router in front of a gateway (not a client): label, address, port and how the line is brought in.
  import { t } from './i18n.svelte.js'
  let { items = [], compact = false } = $props()
  import { modeText, labelText } from './upstream.js'
</script>
{#if items.length}
  <div class="ups" class:compact>
    {#each items as u}
      <div class="up">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 19V5M5 12l7-7 7 7" /></svg>
        <b>{t('Upstream')}: {labelText(u)}</b>
        {#if u.ip}<span class="mono">{u.ip}</span>{/if}
        <span class="muted">{t('on {port} of {dev}', { port: u.port, dev: u.device })}</span>
        {#each u.modes as m}<span class="badge">{modeText(m)}</span>{/each}
      </div>
    {/each}
  </div>
{/if}
<style>
  .ups { display: grid; gap: 6px; margin-bottom: 12px; }
  .up { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; padding: 8px 12px; border: 1px dashed var(--border-2); border-radius: 10px; background: var(--card); }
  .compact .up { border: 0; padding: 0; background: none; }
</style>
