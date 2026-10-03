<script>
  import { onMount } from 'svelte'
  import { bytes } from './format.js'
  import { countryName } from './format.js'
  import { i18n } from './i18n.svelte.js'
  // rows: top-list rows {key: ISO-2 code, up, down, internal}. Countries are shaded by traffic (sqrt scale).
  let { rows = [] } = $props()
  const W = 720, H = 300, LAT0 = 84, KY = H / 138
  let shapes = $state([])
  const total = r => r.up + r.down + r.internal
  const byCC = $derived(new Map(rows.filter(r => r.key).map(r => [r.key.toUpperCase(), total(r)])))
  const max = $derived(Math.max(1, ...byCC.values()))

  onMount(async () => {
    const [{ feature }, topo, iso] = await Promise.all([import('topojson-client'), import('world-atlas/countries-110m.json'), import('./iso-numeric.js')])
    const px = ([lon, lat]) => ((lon + 180) / 360 * W).toFixed(1) + ' ' + ((LAT0 - lat) * KY).toFixed(1)
    const ring = r => 'M' + r.map(px).join('L') + 'Z'
    shapes = feature(topo.default, topo.default.objects.countries).features.filter(f => f.id !== '010').map(f => {
      const g = f.geometry
      const polys = g.type === 'Polygon' ? [g.coordinates] : g.coordinates
      return { cc: iso.default[parseInt(f.id, 10)] || '', d: polys.map(p => p.map(ring).join('')).join('') }
    })
  })
  const fill = cc => (byCC.has(cc) ? 'var(--accent)' : 'var(--border)')
  const op = cc => (byCC.has(cc) ? 0.25 + 0.75 * Math.sqrt(byCC.get(cc) / max) : 0.55)
</script>
<div class="map">
  {#if shapes.length}
    <svg viewBox="0 0 {W} {H}" role="img" aria-label="World map">
      {#each shapes as s}
        <path d={s.d} fill={fill(s.cc)} fill-opacity={op(s.cc)} stroke="var(--card)" stroke-width=".5">
          {#if byCC.has(s.cc)}<title>{countryName(s.cc, i18n.lang)} · {bytes(byCC.get(s.cc))}</title>{/if}
        </path>
      {/each}
    </svg>
  {/if}
</div>
<style>.map { padding: 8px 12px 0; } svg { display: block; width: 100%; height: auto; }</style>
