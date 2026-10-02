<script>
  import { t } from './i18n.svelte.js'
  let { title = '', onclose, wide = false, children, footer } = $props()
  function key(e) { if (e.key === 'Escape') onclose?.() }
</script>
<svelte:window onkeydown={key} />
<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="ov" onclick={e => e.target === e.currentTarget && onclose?.()}>
  <div class="dlg" class:wide role="dialog" aria-modal="true" aria-label={title}>
    <div class="h"><h2>{title}</h2><button class="x" aria-label={t('Close')} onclick={() => onclose?.()}>✕</button></div>
    <div class="b">{@render children?.()}</div>
    {#if footer}<div class="f">{@render footer()}</div>{/if}
  </div>
</div>
<style>
  .ov { position: fixed; inset: 0; background: rgba(0,0,0,.45); z-index: 40; display: grid; place-items: start center; overflow-y: auto; padding: 4vh 16px; }
  .dlg { background: var(--card); border: 1px solid var(--border); border-radius: 12px; width: min(560px, 100%); box-shadow: 0 20px 60px rgba(0,0,0,.35); }
  .dlg.wide { width: min(980px, 100%); }
  .h { display: flex; justify-content: space-between; align-items: center; padding: 14px 18px; border-bottom: 1px solid var(--border); }
  .h h2 { font-size: 16px; }
  .x { border: 0; background: transparent; color: var(--muted); font-size: 16px; padding: 4px 8px; border-radius: 6px; }
  .x:hover { background: var(--card-2); color: var(--text); }
  .b { padding: 18px; }
  .f { padding: 12px 18px; border-top: 1px solid var(--border); display: flex; gap: 10px; justify-content: flex-end; align-items: center; flex-wrap: wrap; }
</style>
