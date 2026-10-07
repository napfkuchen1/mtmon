<script>
  // Tiny trend line for KPI cards. points: numbers, oldest first.
  let { points = [], color = 'var(--accent)', height = 30 } = $props()
  const n = $derived(points.length)
  const max = $derived(Math.max(...points, 1))
  const xy = $derived(points.map((v, i) => [n > 1 ? (i / (n - 1)) * 100 : 50, height - 2 - (v / max) * (height - 6)]))
  const line = $derived(xy.map((p, i) => (i ? 'L' : 'M') + p[0].toFixed(1) + ' ' + p[1].toFixed(1)).join(' '))
  const uid = Math.random().toString(36).slice(2, 7)
</script>
{#if n > 1}
  <svg class="spark" viewBox="0 0 100 {height}" preserveAspectRatio="none" style="height:{height}px" aria-hidden="true">
    <defs><linearGradient id="s{uid}" x1="0" x2="0" y1="0" y2="1"><stop offset="0" stop-color={color} stop-opacity=".3" /><stop offset="1" stop-color={color} stop-opacity="0" /></linearGradient></defs>
    <path d="{line} L100 {height} L0 {height} Z" fill="url(#s{uid})" />
    <path d={line} fill="none" stroke={color} stroke-width="1.6" vector-effect="non-scaling-stroke" stroke-linejoin="round" />
  </svg>
{:else}<div style="height:{height}px"></div>{/if}
<style>.spark { display: block; width: 100%; margin-top: 4px; }</style>
