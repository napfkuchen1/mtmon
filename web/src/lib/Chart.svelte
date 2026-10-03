<script>
  // Lightweight SVG area/line chart. series: [{name,color,points:[{x,y}]}]
  import { t, i18n } from './i18n.svelte.js'
  import { app } from './state.svelte.js'
  let { series = [], height = 190, fmt = v => String(v), xfmt: xf = null, empty = null, bars = false, step = 0 } = $props()
  let w = $state(600)
  const xfmt = x => xf ? xf(x) : new Date(x * 1000).toLocaleTimeString(i18n.locale, { hour12: false, hour: '2-digit', minute: '2-digit', ...(x1 - x0 < 900 ? { second: '2-digit' } : {}) })
  const pad = { l: 78, r: 10, t: 10, b: 22 }
  let hover = $state(null)

  const all = $derived(series.flatMap(s => s.points))
  const xs = $derived(all.map(p => p.x))
  const x0 = $derived(xs.length ? Math.min(...xs) : 0)
  const x1 = $derived(xs.length ? Math.max(...xs) : 1)
  const minDiff = $derived.by(() => { const u = [...new Set(xs)].sort((a, b) => a - b); let m = Infinity; for (let i = 1; i < u.length; i++) m = Math.min(m, u[i] - u[i - 1]); return m })
  const stepV = $derived(step || (isFinite(minDiff) ? minDiff : 3600))
  const lo = $derived(bars ? x0 - stepV / 2 : x1 === x0 ? x0 - 1800 : x0)
  const hi = $derived(bars ? x1 + stepV / 2 : x1 === x0 ? x0 + 1800 : x1)
  const ymax = $derived(niceMax(all.reduce((m, p) => Math.max(m, p.y), 0)))
  const iw = $derived(Math.max(10, w - pad.l - pad.r))
  const ih = $derived(height - pad.t - pad.b)
  const sx = x => pad.l + (hi === lo ? iw / 2 : ((x - lo) / (hi - lo)) * iw)
  const sy = y => pad.t + ih - (ymax ? (y / ymax) * ih : 0)

  function niceMax(v) {
    if (v <= 0) return 1
    const p = Math.pow(10, Math.floor(Math.log10(v)))
    const n = v / p
    return (n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10) * p
  }
  const modern = $derived(app.chart !== 'classic')
  // modern: monotone cubic curve (smooth, never overshoots); classic: straight segments
  function line(pts) {
    if (!modern || pts.length < 3) return pts.map((p, i) => (i ? 'L' : 'M') + sx(p.x).toFixed(1) + ' ' + sy(p.y).toFixed(1)).join(' ')
    const X = pts.map(p => sx(p.x)), Y = pts.map(p => sy(p.y)), n = pts.length
    const dx = [], m = []
    for (let i = 0; i < n - 1; i++) { dx[i] = X[i + 1] - X[i] || 1e-6; m[i] = (Y[i + 1] - Y[i]) / dx[i] }
    const tg = [m[0]]
    for (let i = 1; i < n - 1; i++) tg[i] = m[i - 1] * m[i] <= 0 ? 0 : (m[i - 1] + m[i]) / 2
    tg[n - 1] = m[n - 2]
    for (let i = 0; i < n - 1; i++) if (m[i] === 0) { tg[i] = 0; tg[i + 1] = 0 } else { const a = tg[i] / m[i], b = tg[i + 1] / m[i], h = Math.hypot(a, b); if (h > 3) { tg[i] = 3 * a / h * m[i]; tg[i + 1] = 3 * b / h * m[i] } }
    let d = 'M' + X[0].toFixed(1) + ' ' + Y[0].toFixed(1)
    for (let i = 0; i < n - 1; i++) d += `C${(X[i] + dx[i] / 3).toFixed(1)} ${(Y[i] + tg[i] * dx[i] / 3).toFixed(1)} ${(X[i + 1] - dx[i] / 3).toFixed(1)} ${(Y[i + 1] - tg[i + 1] * dx[i] / 3).toFixed(1)} ${X[i + 1].toFixed(1)} ${Y[i + 1].toFixed(1)}`
    return d
  }
  function area(pts) {
    if (!pts.length) return ''
    return line(pts) + ` L${sx(pts[pts.length - 1].x).toFixed(1)} ${sy(0)} L${sx(pts[0].x).toFixed(1)} ${sy(0)} Z`
  }
  // night bands (20:00–07:00 local) when the chart spans more than half a day, so days are easy to tell apart
  const nights = $derived.by(() => {
    if (!modern || bars || !xs.length || hi - lo < 12 * 3600) return []
    const out = []; const d = new Date(lo * 1000); d.setHours(20, 0, 0, 0)
    for (let t0 = d.getTime() / 1000 - 86400; t0 < hi; t0 += 86400) { const a = Math.max(lo, t0), b = Math.min(hi, t0 + 11 * 3600); if (b > a) out.push([sx(a), sx(b)]) }
    return out
  })
  const peaks = $derived(modern && !bars ? series.map(s => s.points.reduce((m, p) => (!m || p.y > m.y ? p : m), null)).map((p, i) => (p && p.y > 0 ? { p, c: series[i].color } : null)).filter(Boolean) : [])
  const ticks = $derived([0, .25, .5, .75, 1].map(f => f * ymax))
  const xfr = $derived(w < 480 ? [0, .5, 1] : [0, .25, .5, .75, 1]) // fewer labels on narrow screens so they do not overlap
  const nx = $derived(new Set(xs).size)
  const xticks = $derived(xs.length ? xfr.map(f => lo + f * (hi - lo)).filter((t, i, a) => i === 0 || xfmt(t) !== xfmt(a[i - 1])) : [])

  function move(e) {
    if (!xs.length) return
    const r = e.currentTarget.getBoundingClientRect()
    const x = lo + ((e.clientX - r.left - pad.l) / iw) * (hi - lo)
    let best = null
    for (const s of series) for (const p of s.points) if (!best || Math.abs(p.x - x) < Math.abs(best - x)) best = p.x
    hover = best
  }
  const hv = $derived(hover === null ? [] : series.map(s => ({ s, p: s.points.find(p => p.x === hover) })).filter(o => o.p))
  const uid = Math.random().toString(36).slice(2, 8)
</script>

<div class="wrap" bind:clientWidth={w}>
  {#if !all.length}
    <div class="empty" style="height:{height}px;display:flex;align-items:center;justify-content:center">{empty ?? t('No data yet')}</div>
  {:else if !bars && nx < 2}
    <div class="empty" style="height:{height}px;display:flex;align-items:center;justify-content:center">{t('Collecting data… the chart appears after a few minutes')}</div>
  {:else}
    <svg width={w} {height} role="img" aria-label={t('Traffic chart')} onmousemove={move} onmouseleave={() => (hover = null)}>
      <defs>
        {#each series as s, i}
          <linearGradient id="g{uid}{i}" x1="0" x2="0" y1="0" y2="1">
            <stop offset="0" stop-color={s.color} stop-opacity={modern ? '.38' : '.28'} /><stop offset="1" stop-color={s.color} stop-opacity="0" />
          </linearGradient>
        {/each}
      </defs>
      {#each nights as n}<rect x={n[0]} y={pad.t} width={n[1] - n[0]} height={ih} fill="var(--card-2)" opacity=".7" />{/each}
      {#each ticks as t}
        <line x1={pad.l} x2={w - pad.r} y1={sy(t)} y2={sy(t)} stroke="var(--border)" stroke-opacity={modern ? '.7' : '1'} stroke-dasharray={t && !modern ? '3 4' : ''} />
        <text x={pad.l - 8} y={sy(t) + 4} text-anchor="end" fill="var(--muted)" font-size="11">{fmt(t)}</text>
      {/each}
      {#each xticks as t, i}
        <text x={sx(t)} y={height - 5} text-anchor={i === 0 ? 'start' : i === xticks.length - 1 ? 'end' : 'middle'} fill="var(--muted)" font-size="11">{xfmt(t)}</text>
      {/each}
      {#each series as s, i}
        {#if bars}
          {@const bw = Math.max(2, (sx(x0 + stepV) - sx(x0)) * 0.8 / series.length)}
          {#each s.points as p}<rect x={sx(p.x) - (bw * series.length) / 2 + i * bw} y={sy(p.y)} width={bw} height={Math.max(0, sy(0) - sy(p.y))} fill={s.color} rx="2" />{/each}
        {:else}
          <path d={area(s.points)} fill="url(#g{uid}{i})" />
          <path d={line(s.points)} fill="none" stroke={s.color} stroke-width={modern ? 2.2 : 1.8} stroke-linejoin="round" stroke-linecap="round" />
          {#if s.points.length === 1}<circle cx={sx(s.points[0].x)} cy={sy(s.points[0].y)} r="3.5" fill={s.color} />{/if}
        {/if}
      {/each}
      {#each peaks as k}
        <circle cx={sx(k.p.x)} cy={sy(k.p.y)} r="3" fill={k.c} stroke="var(--card)" stroke-width="1.5" />
        <text x={Math.min(Math.max(sx(k.p.x), pad.l + 30), w - pad.r - 30)} y={Math.max(sy(k.p.y) - 7, 11)} text-anchor="middle" font-size="10.5" fill={k.c}>{fmt(k.p.y)}</text>
      {/each}
      {#if hover !== null}
        <line x1={sx(hover)} x2={sx(hover)} y1={pad.t} y2={pad.t + ih} stroke="var(--border-2)" />
        {#each hv as o}<circle cx={sx(hover)} cy={sy(o.p.y)} r="3.5" fill={o.s.color} stroke="var(--card)" stroke-width="1.5" />{/each}
      {/if}
    </svg>
    {#if hover !== null}
      <div class="tip" style="left:{Math.min(Math.max(sx(hover) + 12, 8), w - 170)}px">
        <div class="muted">{xfmt(hover)}</div>
        {#each hv as o}<div><i style="background:{o.s.color}"></i>{o.s.name}: <b class="num">{fmt(o.p.y)}</b></div>{/each}
      </div>
    {/if}
  {/if}
</div>

<style>
  .wrap { position: relative; width: 100%; }
  svg { display: block; }
  .tip { position: absolute; top: 8px; pointer-events: none; background: var(--card); border: 1px solid var(--border-2); border-radius: 8px; padding: 6px 10px; font-size: 12.5px; box-shadow: 0 6px 18px rgba(0,0,0,.12); min-width: 140px; }
  .tip i { display: inline-block; width: 8px; height: 8px; border-radius: 2px; margin-right: 6px; }
</style>
