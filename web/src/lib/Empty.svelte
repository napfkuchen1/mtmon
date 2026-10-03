<script>
  import { ICONS } from './icons.js'
  // Friendly empty state: icon, headline, hint and an optional next step (link or button).
  let { icon = 'generic', title = '', hint = '', href = '', action = '', onaction = null, compact = false } = $props()
</script>
<div class="emp" class:compact>
  <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="var(--faint)" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d={ICONS[icon] || ICONS.generic} /></svg>
  {#if title}<b>{title}</b>{/if}
  {#if hint}<span class="muted">{hint}</span>{/if}
  {#if action && href}<a class="btn sm" {href}>{action}</a>{:else if action}<button class="btn sm" onclick={() => onaction?.()}>{action}</button>{/if}
</div>
<style>
  .emp { display: flex; flex-direction: column; align-items: center; gap: 6px; padding: 30px 16px; text-align: center; }
  .emp.compact { padding: 18px 12px; } .emp.compact svg { width: 28px; height: 28px; }
  .emp b { color: var(--text); font-weight: 600; } .emp .btn { margin-top: 6px; }
</style>
