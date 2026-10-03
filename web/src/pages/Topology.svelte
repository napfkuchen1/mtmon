<script>
  import { api, download } from '../lib/api.js'
  import { go } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { t, tr } from '../lib/i18n.svelte.js'
  import { bps, deviceKind } from '../lib/format.js'
  import { ICONS } from '../lib/icons.js'
  import Empty from '../lib/Empty.svelte'

  const d = poll(() => api('/topology'), 5000)
  const CW = 168, RH = 52, PAD = 16, GP = 12 // client cell width / row height, outer padding, container padding
  let W = $state(900)

  // collapsed groups survive reloads
  let collapsed = $state((() => { try { return JSON.parse(localStorage.getItem('mtmon.topo.collapsed') || '{}') } catch { return {} } })())
  const save = () => { try { localStorage.setItem('mtmon.topo.collapsed', JSON.stringify(collapsed)) } catch {} }
  function toggle(id) { collapsed[id] = !collapsed[id]; save() }
  function setAll(v) { for (const g of layout.groups) collapsed[g.dv.id] = v && g.kids.length > 0; save() }

  // device tree: BFS from the router over the neighbor links; a link that is only visible "through" another
  // device (e.g. one AP seeing the other AP via the switch) is never part of the tree.
  const tree = $derived.by(() => {
    const nodes = d.data?.nodes || [], edges = d.data?.edges || []
    const devs = nodes.filter(n => n.type !== 'client')
    const root = devs.find(n => n.type === 'router') || devs[0]
    const parent = new Map(), label = new Map(), order = []
    if (root) {
      const seen = new Set([root.id]); const q = [root.id]
      while (q.length) {
        const cur = q.shift(); order.push(cur)
        for (const e of edges) {
          if (!e.from.startsWith('d:') || !e.to.startsWith('d:') || e.from !== cur || seen.has(e.to)) continue
          seen.add(e.to); parent.set(e.to, cur); label.set(e.to, e.label); q.push(e.to)
        }
      }
      for (const dv of devs) if (!seen.has(dv.id)) { parent.set(dv.id, root.id); order.push(dv.id) } // no neighbor info: hang off the router
    }
    return { devs, root, parent, label, order }
  })

  const maxBps = $derived(Math.max(1, ...(d.data?.nodes || []).map(n => n.bps)))
  const palette = ['var(--accent)', 'var(--down)', '#a855f7', '#14b8a6', '#ec4899', '#eab308']
  const ssids = $derived([...new Set((d.data?.nodes || []).filter(n => n.wifi && n.ssid).map(n => n.ssid))].sort())
  const dotCol = n => (n.wifi ? palette[Math.max(0, ssids.indexOf(n.ssid)) % palette.length] : 'var(--ok)')
  const where = n => (n.wifi ? [n.ssid, n.band].filter(Boolean).join(' · ') : n.port)
  const kindOf = n => deviceKind({ label: n.label, vendor: n.vendor, wifi: n.wifi })
  const short = (s, n) => (s.length > n ? s.slice(0, n - 1) + '…' : s)

  // layout: router on top; every other device is a group (device node + container with its clients). Groups wrap
  // into bands so the page never needs horizontal scrolling.
  const layout = $derived.by(() => {
    const nodes = d.data?.nodes || [], edges = d.data?.edges || []
    const { devs, root, parent, label, order } = tree
    if (!root) return { groups: [], pos: new Map(), height: 0, edgesOut: [] }
    const cl = nodes.filter(n => n.type === 'client')
    const cparent = new Map(edges.filter(e => e.to.startsWith('c:')).map(e => [e.to, e.from]))
    const inner = Math.max(CW + 2 * GP, W - 2 * PAD)
    const maxCols = Math.max(1, Math.min(4, Math.floor((inner - 2 * GP) / CW)))
    const sortKids = (a, b) => (a.wifi - b.wifi) || ((a.wifi ? a.ssid : a.port) || '').localeCompare((b.wifi ? b.ssid : b.port) || '') || a.label.toLowerCase().localeCompare(b.label.toLowerCase())
    const byId = new Map(devs.map(n => [n.id, n]))
    const orphans = cl.filter(c => !cparent.has(c.id) || !byId.has(cparent.get(c.id)))
    // groups: the router's own clients first, then the other devices in tree order
    const groups = order.map(id => byId.get(id)).filter(Boolean).map(dv => {
      let kids = cl.filter(c => cparent.get(c.id) === dv.id)
      if (dv === root) kids = kids.concat(orphans)
      kids.sort(sortKids)
      const isCollapsed = !!collapsed[dv.id]
      const cols = Math.max(1, Math.min(maxCols, Math.ceil(kids.length / 5))) // about 5 rows per group, wider only when needed
      const rows = isCollapsed ? 0 : Math.ceil(kids.length / cols)
      const cw = Math.max(isCollapsed ? 170 : cols * CW + 2 * GP, 176)
      const ch = kids.length ? 34 + (isCollapsed ? 0 : rows * RH + 6) : 0
      return { dv, kids, cols, rows, cw, ch, isCollapsed, bps: kids.reduce((s, k) => s + k.bps, 0) }
    })
    const pos = new Map()
    pos.set(root.id, { x: W / 2, y: 44 })
    // pack groups into bands (rows of groups) that fit the width, then centre every band
    const bands = []; let cur = [], curW = 0
    for (const g of groups) {
      if (cur.length && curW + g.cw > inner) { bands.push(cur); cur = []; curW = 0 }
      cur.push(g); curW += g.cw + 16
    }
    if (cur.length) bands.push(cur)
    let y = 112
    for (const bnd of bands) {
      const total = bnd.reduce((s, g) => s + g.cw + 16, -16)
      let x = PAD + Math.max(0, (inner - total) / 2), bandH = 0
      for (const g of bnd) {
        const isRoot = g.dv === root
        g.nodeH = isRoot ? 0 : 66 // device node sits above its container
        if (!isRoot) pos.set(g.dv.id, { x: x + g.cw / 2, y: y + 24 })
        g.cx = x; g.cy = y + g.nodeH // container top-left
        g.kids.forEach((k, i) => pos.set(k.id, { x: x + GP + (i % g.cols) * CW + CW / 2, y: g.cy + 34 + Math.floor(i / g.cols) * RH + 22 }))
        bandH = Math.max(bandH, g.nodeH + g.ch)
        x += g.cw + 16
      }
      y += bandH + 26
    }
    // edges: parent device -> device node (with port label), device -> its container
    const edgesOut = []
    for (const g of groups) {
      const isRoot = g.dv === root
      if (!isRoot) {
        const p = pos.get(parent.get(g.dv.id)), c = pos.get(g.dv.id)
        if (p && c) edgesOut.push({ id: 'e:' + g.dv.id, from: parent.get(g.dv.id), to: g.dv.id, a: { x: p.x, y: p.y + 24 }, b: { x: c.x, y: c.y - 24 }, label: label.get(g.dv.id) || '', kind: 'uplink', bps: g.dv.bps })
      }
      if (g.kids.length) {
        const dp = pos.get(g.dv.id)
        edgesOut.push({ id: 'g:' + g.dv.id, from: g.dv.id, to: 'grp:' + g.dv.id, a: { x: dp.x, y: dp.y + 24 }, b: { x: g.cx + g.cw / 2, y: g.cy }, label: '', kind: g.kids.every(k => k.wifi) ? 'wifi' : 'wired', bps: g.bps })
      }
    }
    return { groups, pos, height: Math.max(y, 200), edgesOut, root }
  })

  // hover: highlight the path client -> device(s) -> router
  let hov = $state('')
  const chain = $derived.by(() => {
    if (!hov) return new Set()
    const s = new Set([hov])
    let cur = hov
    if (cur.startsWith('c:')) { const p = (d.data?.edges || []).find(e => e.to === cur); if (p) { cur = p.from; s.add(cur); s.add('grp:' + cur) } }
    while (tree.parent.has(cur)) { cur = tree.parent.get(cur); s.add(cur) }
    return s
  })
  const dim = id => hov && !chain.has(id)
  const ew = e => 1.2 + 3 * Math.min(1, (e.bps || 0) / (maxBps * 3))
  const col = e => (e.kind === 'wifi' ? 'var(--accent)' : 'var(--border-2)')
  const edgeOn = e => chain.has(e.to) && chain.has(e.from)
  const open = n => go((n.type === 'client' ? 'client/' : 'device/') + encodeURIComponent(n.id.slice(2)))
</script>

<div class="head"><h1>{t('Topology')}</h1><span class="muted">{t('from LLDP/MNDP neighbors, wifi registrations and bridge hosts')}</span>
  <span class="tools">
    <button class="btn sm" onclick={() => setAll(true)}>{t('Collapse all')}</button>
    <button class="btn sm" onclick={() => setAll(false)}>{t('Expand all')}</button>
    <button class="btn sm" title={t('Raw tables mtmon got from your devices (Wi-Fi registrations, bridge hosts, ARP, DHCP) – for troubleshooting')} onclick={() => download('/diagnostics')}>{t('Diagnostics')}</button>
  </span>
  <div class="legend"><span><i style="background:var(--ok)"></i>{t('Wired')}</span>
    {#each ssids as sid}<span><i style="background:{palette[ssids.indexOf(sid) % palette.length]};border-radius:50%"></i>{sid}</span>{/each}</div></div>
{#if d.error}<div class="warnbox">{tr(d.error)}</div>{/if}
<div class="card wrap" bind:clientWidth={W}>
  {#if d.data && d.data.nodes.length}
    <svg width={W} height={layout.height} role="img" aria-label={t('Network topology')}>
      {#each layout.edgesOut as e (e.id)}
        <path d="M{e.a.x} {e.a.y} C{e.a.x} {(e.a.y + e.b.y) / 2}, {e.b.x} {(e.a.y + e.b.y) / 2}, {e.b.x} {e.b.y}" fill="none" stroke={edgeOn(e) ? 'var(--accent)' : col(e)}
          stroke-width={edgeOn(e) ? 3 : ew(e)} stroke-dasharray={e.kind === 'wifi' ? '5 4' : ''} opacity={hov ? (edgeOn(e) ? 1 : 0.18) : 0.85} />
        {#if e.label}<g transform="translate({(e.a.x + e.b.x) / 2},{(e.a.y + e.b.y) / 2})" opacity={hov && !edgeOn(e) ? 0.3 : 1}><rect x="-27" y="-9" width="54" height="18" rx="9" fill="var(--card)" stroke="var(--border-2)" /><text text-anchor="middle" y="4" font-size="10.5" fill="var(--muted)">{e.label.split(',')[0].slice(-9)}</text></g>{/if}
      {/each}

      {#each layout.groups as g (g.dv.id)}
        {#if g.kids.length}
          <g opacity={hov && dim('grp:' + g.dv.id) && dim(g.dv.id) ? 0.4 : 1}>
            <rect x={g.cx} y={g.cy} width={g.cw} height={g.ch} rx="12" fill="var(--card-2)" stroke={chain.has('grp:' + g.dv.id) ? 'var(--accent)' : 'var(--border)'} />
            <g class="gh" role="button" tabindex="0" onclick={() => toggle(g.dv.id)} onkeydown={e => e.key === 'Enter' && toggle(g.dv.id)}>
              <rect x={g.cx} y={g.cy} width={g.cw} height="30" rx="12" fill="transparent" />
              <path d={g.isCollapsed ? 'M9 6l6 6-6 6' : 'M6 9l6 6 6-6'} transform="translate({g.cx + 8},{g.cy + 3}) scale(.8)" fill="none" stroke="var(--muted)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
              <text x={g.cx + 28} y={g.cy + 19} font-size="12" font-weight="600" fill="var(--muted)">{g.kids.length} {g.kids.length === 1 ? t('device') : t('devices')}{g.isCollapsed ? ' · ' + t('click to expand') : ''}</text>
            </g>
          </g>
        {/if}
      {/each}

      {#each layout.groups as g (g.dv.id)}
        {@const n = g.dv}
        {@const p = layout.pos.get(n.id)}
        {#if p}
          <g transform="translate({p.x},{p.y})" class="node" role="button" tabindex="0" opacity={dim(n.id) ? 0.4 : 1} onpointerenter={() => (hov = n.id)} onpointerleave={() => (hov = '')}
            onclick={() => open(n)} onkeydown={e => e.key === 'Enter' && open(n)}>
            <title>{n.label} · {n.sub} · {bps(n.bps)}</title>
            <rect x="-82" y="-24" width="164" height="48" rx="11" fill={n.status === 'up' ? 'var(--card)' : 'var(--bad-bg)'} stroke={n.type === 'router' ? 'var(--accent)' : 'var(--border-2)'} stroke-width="2" />
            <g transform="translate(-72,-9) scale(.75)" fill="none" stroke={n.status === 'up' ? 'var(--ok)' : 'var(--bad)'} stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d={n.type === 'ap' ? ICONS.ap : ICONS.network} /></g>
            <text x="-48" y="-2" font-size="13.5" font-weight="700" fill="var(--text)">{short(n.label, 18)}</text>
            <text x="-48" y="14" font-size="11" fill="var(--muted)">{n.type} · {n.sub}</text>
          </g>
        {/if}
        {#if !g.isCollapsed}
          {#each g.kids as k (k.id)}
            {@const q = layout.pos.get(k.id)}
            {#if q}
              <g transform="translate({q.x},{q.y})" class="node" role="button" tabindex="0" opacity={dim(k.id) ? 0.35 : 1} onpointerenter={() => (hov = k.id)} onpointerleave={() => (hov = '')}
                onclick={() => open(k)} onkeydown={e => e.key === 'Enter' && open(k)}>
                <title>{k.label} · {k.sub}{where(k) ? ' · ' + where(k) : ''} · {bps(k.bps)}</title>
                <rect x="-76" y="-19" width="152" height="38" rx="9" fill="var(--card)" stroke={chain.has(k.id) ? 'var(--accent)' : 'var(--border-2)'} stroke-width={chain.has(k.id) ? 2 : 1} />
                <g transform="translate(-70,-9) scale(.75)" fill="none" stroke={dotCol(k)} stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d={ICONS[kindOf(k)] || ICONS.generic} /></g>
                <text x="-46" y="-2" font-size="12.5" font-weight="600" fill="var(--text)">{short(k.label, 17)}</text>
                <text x="-46" y="12" font-size="10.5" fill="var(--muted)">{short(k.port ? k.sub + ' · ' + k.port : k.sub, 25)}</text>
              </g>
            {/if}
          {/each}
        {/if}
      {/each}
    </svg>
  {:else}<Empty icon="network" title={d.loading ? t('Loading…') : t('No devices or clients yet')} hint={d.loading ? '' : t('Add a router under Devices – clients show up here as soon as they are seen.')} href="#/devices" action={d.loading ? '' : t('Open Devices')} />{/if}
</div>
<style>
  .head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 18px; flex-wrap: wrap; }
  .tools { display: inline-flex; gap: 8px; margin-left: auto; }
  .wrap { padding: 4px 0; overflow: hidden; }
  .node { cursor: pointer; } .node:hover rect { stroke: var(--accent); }
  .gh { cursor: pointer; } .gh:hover text { fill: var(--text); }
  svg { display: block; } .node, path { transition: opacity .12s; }
</style>
