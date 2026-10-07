<script>
  // Country flag as a real image: flag emoji are not rendered on Windows (they show as the letters "DE").
  // SVGs are loaded lazily, one tiny chunk per country, so only the flags that are actually shown get downloaded.
  const loaders = import.meta.glob('/node_modules/country-flag-icons/3x2/*.svg', { query: '?url', import: 'default' })
  const cache = new Map()
  let { cc = '', size = 16 } = $props()
  const code = $derived((cc || '').toUpperCase())
  let src = $state('')
  $effect(() => {
    const c = code
    src = cache.get(c) || ''
    if (src || c.length !== 2) return
    const load = loaders[`/node_modules/country-flag-icons/3x2/${c}.svg`]
    let stop = false
    load?.().then(u => { cache.set(c, u); if (!stop) src = u }).catch(() => {})
    return () => { stop = true }
  })
</script>
{#if code.length === 2}
  {#if src}<img class="flag" {src} alt={code} width={size} height={Math.round(size * 2 / 3)} loading="lazy" />{:else}<span class="flag ph" style="width:{size}px;height:{Math.round(size * 2 / 3)}px"></span>{/if}
{/if}
<style>
  .flag { display: inline-block; vertical-align: -2px; border-radius: 2px; box-shadow: 0 0 0 1px var(--border-2); object-fit: cover; margin-right: 6px; }
  .ph { background: var(--card-2); }
</style>
