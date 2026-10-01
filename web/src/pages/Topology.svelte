<script>
  import { api } from '../lib/api.js'
  import { go } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { bps } from '../lib/format.js'

  const d = poll(() => api('/topology'), 5000)
  const CW = 168, RH = 54, PER_ROW = 4

  const layout = $derived.by(() => {
    const nodes = d.data?.nodes || [], edges = d.data?.edges || []
    const devs = nodes.filter(n => n.type !== 'client')
    const cl = nodes.filter(n => n.type === 'client')
    const parent = new Map(edges.filter(e => e.to.startsWith('c:')).map(e => [e.to, e.from]))
    const groups = devs.map(dv => ({ dv, kids: cl.filter(c => parent.get(c.id) === dv.id) }))
    const orphans = cl.filter(c => !parent.has(c.id))
    if (orphans.length) groups.push({ dv: null, kids: orphans })
    let x = 20
    const pos = new Map(), out = []
    for (const g of groups) {
      const cols = Math.max(1, Math.min(PER_ROW, g.kids.length))
      const w = cols * CW
      g.kids.forEach((k, i) => pos.set(k.id, { x: x + (i % cols) * CW + CW / 2, y: 330 + Math.floor(i / cols) * RH }))
      if (g.dv) pos.set(g.dv.id, { x: x + w / 2, y: g.dv.type === 'router' ? 50 : 185 })
      out.push({ x, w })
      x += w + 36
    }
    const rows = Math.max(1, ...groups.map(g => Math.ceil(g.kids.length / Math.min(PER_ROW, Math.max(1, g.kids.length)))))
    return { nodes, edges, pos, width: Math.max(x, 600), height: 330 + rows * RH + 30 }
  })
  const maxBps = $derived(Math.max(1, ...(d.data?.nodes || []).map(n => n.bps)))
  const w = e => 1 + 3 * Math.min(1, (d.data.nodes.find(n => n.id === e.to)?.bps || 0) / maxBps)
  const col = e => e.kind === 'wifi' ? 'var(--accent)' : 'var(--border-2)'
</script>

<div class="head"><h1>Topology</h1><span class="muted">from LLDP/MNDP neighbors, wifi registrations and bridge hosts</span>
  <div class="legend" style="margin-left:auto"><span><i style="background:var(--accent)"></i>Wi-Fi</span><span><i style="background:var(--border-2)"></i>Wired / uplink</span></div></div>
{#if d.error}<div class="warnbox">{d.error}</div>{/if}
<div class="card scroll" style="padding:12px">
  {#if d.data && d.data.nodes.length}
    <svg width={layout.width} height={layout.height} role="img" aria-label="Network topology">
      {#each layout.edges as e}
        {@const a = layout.pos.get(e.from)} {@const b = layout.pos.get(e.to)}
        {#if a && b}<path d="M{a.x} {a.y + 18} C{a.x} {(a.y + b.y) / 2}, {b.x} {(a.y + b.y) / 2}, {b.x} {b.y - 18}" fill="none" stroke={col(e)} stroke-width={w(e)} stroke-dasharray={e.kind === 'wifi' ? '5 4' : ''} opacity=".85" />{/if}
      {/each}
      {#each layout.nodes as n (n.id)}
        {@const p = layout.pos.get(n.id)}
        {#if p}
          <g transform="translate({p.x},{p.y})" class="node" role="button" tabindex="0" onclick={() => go((n.type === 'client' ? 'client/' : 'device/') + encodeURIComponent(n.id.slice(2)))} onkeydown={e => e.key === 'Enter' && go((n.type === 'client' ? 'client/' : 'device/') + encodeURIComponent(n.id.slice(2)))}>
            <title>{n.label} · {n.sub} · {bps(n.bps)}</title>
            {#if n.type === 'client'}
              <rect x="-76" y="-19" width="152" height="38" rx="9" fill="var(--card)" stroke="var(--border-2)" />
              <circle cx="-62" cy="-4" r="4" fill={n.wifi ? 'var(--accent)' : 'var(--ok)'} />
              <text x="-52" y="0" font-size="12.5" font-weight="600" fill="var(--text)">{n.label.length > 18 ? n.label.slice(0, 17) + '…' : n.label}</text>
              <text x="-52" y="13" font-size="10.5" fill="var(--muted)">{n.sub.length > 26 ? n.sub.slice(0, 25) + '…' : n.sub}</text>
            {:else}
              <rect x="-82" y="-24" width="164" height="48" rx="11" fill={n.status === 'up' ? 'var(--card)' : 'var(--bad-bg)'} stroke={n.type === 'router' ? 'var(--accent)' : 'var(--border-2)'} stroke-width="2" />
              <circle cx="-66" cy="-6" r="5" fill={n.status === 'up' ? 'var(--ok)' : 'var(--bad)'} />
              <text x="-54" y="-2" font-size="13.5" font-weight="700" fill="var(--text)">{n.label}</text>
              <text x="-54" y="14" font-size="11" fill="var(--muted)">{n.type} · {n.sub}</text>
            {/if}
          </g>
        {/if}
      {/each}
    </svg>
  {:else}<div class="empty">{d.loading ? 'Loading…' : 'No devices or clients yet'}</div>{/if}
</div>
<style>.head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 18px; flex-wrap: wrap; } .node { cursor: pointer; } .node:hover rect { stroke: var(--accent); }</style>
