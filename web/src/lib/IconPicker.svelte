<script>
  import { ICONS, ICON_CHOICES } from './icons.js'
  import { t } from './i18n.svelte.js'
  // Popover grid to pick a device icon. current: user's choice ("" = automatic).
  let { current = '', onpick, onclose } = $props()
  const labels = $derived({ phone: t('Phone'), tablet: t('Tablet'), laptop: t('Laptop'), desktop: t('Desktop'), tv: t('TV'), speaker: t('Speaker'), printer: t('Printer'), printer3d: t('3D printer'), camera: t('Camera'),
    console: t('Console'), bulb: t('Light / smart home'), iot: t('IoT device'), server: t('Server / NAS'), network: t('Router / switch'), ap: t('Access point'), watch: t('Watch'), car: t('Car / charger'), generic: t('Other') })
</script>
<svelte:window onkeydown={e => e.key === 'Escape' && onclose?.()} />
<button class="shield" aria-label={t('Close')} onclick={() => onclose?.()}></button>
<div class="pop" role="dialog" aria-label={t('Choose an icon')}>
  <div class="grid">
    {#each ICON_CHOICES as k}
      <button class="ic" class:on={current === k} title={labels[k]} aria-label={labels[k]} onclick={() => onpick(k)}>
        <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d={ICONS[k]} /></svg><span>{labels[k]}</span></button>
    {/each}
  </div>
  <button class="btn sm auto" class:primary={!current} onclick={() => onpick('')}>{t('Automatic (guess from name and vendor)')}</button>
</div>
<style>
  .shield { position: fixed; inset: 0; background: transparent; border: 0; z-index: 20; cursor: default; }
  .pop { position: absolute; z-index: 21; top: 100%; left: 0; margin-top: 8px; width: min(420px, 90vw); background: var(--card); border: 1px solid var(--border-2); border-radius: 12px; padding: 12px; box-shadow: 0 12px 40px rgba(0,0,0,.25); }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(88px, 1fr)); gap: 6px; margin-bottom: 10px; }
  .ic { display: flex; flex-direction: column; align-items: center; gap: 4px; padding: 8px 4px; border-radius: 8px; border: 1px solid var(--border); background: var(--card-2); color: var(--text); cursor: pointer; font: inherit; }
  .ic span { font-size: 11px; color: var(--muted); text-align: center; line-height: 1.2; } .ic:hover { border-color: var(--accent); }
  .ic.on { border-color: var(--accent); background: var(--accent-bg); } .auto { width: 100%; }
</style>
