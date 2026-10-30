<script>
  import Avatar from '../lib/Avatar.svelte'
  import Empty from '../lib/Empty.svelte'
  import { api, download } from '../lib/api.js'
  import { app, go, toast } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { t, tr } from '../lib/i18n.svelte.js'
  import { bps, bytes, ago, display, signalQuality } from '../lib/format.js'
  import CleanupDialog from '../lib/CleanupDialog.svelte'
  import { untrack } from 'svelte'
  import { loadSort, saveSort } from '../lib/sortpref.js'
  let cleanup = $state(false)

  let q = $state(''), kind = $state(app.route.params.id === 'dns-bypass' ? 'dnsbypass' : 'all'), sortKey = $state(loadSort('clients', { key: 'name', desc: false }).key), desc = $state(loadSort('clients', { key: 'name', desc: false }).desc)
  let sel = $state({}) // mac -> true: selected for the bulk action
  let grp = $state('') // group filter: '' all, 'none', or a group name
  const gl = poll(() => api('/groups'), 30000)
  const wh = poll(() => api('/wifi/health'), 60000)
  const health = $derived(Object.fromEntries((wh.data?.clients ?? []).map(h => [h.mac, h])))
  const groups = $derived((gl.data?.groups || []).map(g => g.name))
  const d = poll(() => api('/clients?range=' + (app.range === 'live' ? '1h' : app.range)), 5000)

  // live values (rate, usage, signal) are sorted on a snapshot that is refreshed every 10 s, so the list does not
  // re-order itself on every poll
  let snap = $state({})
  const take = () => untrack(() => { const o = {}; for (const x of all) o[x.c.mac] = { live: x.live, usage: x.bytes_up + x.bytes_down, signal: x.c.signal || -999 }; snap = o })
  $effect(() => { sortKey; desc; take(); const id = setInterval(take, 10000); return () => clearInterval(id) })
  const all = $derived.by(() => {
    const live = new Map(app.live.clients.map(c => [c.key, c]))
    return (d.data || []).map(x => ({ ...x, c: x.client, live: (live.get(x.client.mac)?.up_bps || 0) + (live.get(x.client.mac)?.down_bps || 0) || x.up_bps + x.down_bps }))
  })
  const rows = $derived.by(() => {
    let r = all.slice()
    const s = q.trim().toLowerCase()
    if (s) r = r.filter(x => [x.c.mac, x.c.ip, x.c.hostname, x.c.label, x.c.vendor, x.c.device, x.c.ssid].join(' ').toLowerCase().includes(s))
    if (kind === 'online') r = r.filter(x => x.c.online)
    if (kind === 'wifi') r = r.filter(x => x.c.wifi && x.c.online)
    if (kind === 'wired') r = r.filter(x => !x.c.wifi && x.c.online)
    if (kind === 'offline') r = r.filter(x => !x.c.online)
    if (kind === 'watched') r = r.filter(x => x.c.watch)
    if (kind === 'dnsbypass') r = r.filter(x => x.dns_bypass?.length)
    if (grp === 'none') r = r.filter(x => !x.c.group)
    else if (grp) r = r.filter(x => x.c.group === grp)
    const val = x => ({ name: display(x.c).toLowerCase(), ip: x.c.ip.split('.').map(n => n.padStart(3, '0')).join('.'), live: snap[x.c.mac]?.live ?? x.live, usage: snap[x.c.mac]?.usage ?? x.bytes_up + x.bytes_down, signal: snap[x.c.mac]?.signal ?? (x.c.signal || -999), seen: x.c.last_seen, device: x.c.device }[sortKey])
    r.sort((a, b) => { const A = val(a), B = val(b); return (A < B ? -1 : A > B ? 1 : 0) * (desc ? -1 : 1) })
    return r
  })
  function sort(k) { if (sortKey === k) desc = !desc; else { sortKey = k; desc = k !== 'name' && k !== 'ip' && k !== 'device' }
    saveSort('clients', sortKey, desc) }
  const arrow = k => sortKey === k ? (desc ? ' ↓' : ' ↑') : ''
  const kinds = $derived([['all', t('All')], ['online', t('Online')], ['wifi', t('Wi-Fi')], ['wired', t('Wired')], ['offline', t('Offline')], ['watched', '★ ' + t('Watched')], ['dnsbypass', t('DNS bypass')]])
  const picked = $derived(Object.keys(sel).filter(k => sel[k]))
  async function groupSelected(group, auto = false) {
    try { const r = await api('/clients/group', { method: 'POST', body: auto ? { macs: picked, auto: true } : { macs: picked, group } }); sel = {}; d.data = await api('/clients?range=' + (app.range === 'live' ? '1h' : app.range)); toast(t('{n} clients updated', { n: r.count })) }
    catch (e) { toast(tr(e.message)) }
  }
  async function watchSelected(on) {
    try { await api('/clients/watch', { method: 'POST', body: { macs: picked, watch: on } }); sel = {}; d.data = await api('/clients?range=' + (app.range === 'live' ? '1h' : app.range)); toast(t('Saved')) }
    catch (e) { toast(tr(e.message)) }
  }
  const counts = $derived({ all: d.data?.length || 0, online: (d.data || []).filter(x => x.client.online).length })
</script>

<div class="head"><h1>{t('Clients')}</h1><span class="muted">{t('{online} online · {all} known', { online: counts.online, all: counts.all })}</span>
  <span style="flex:1"></span><a class="btn" href="#/inbox">{t('New devices')}</a> <button class="btn" onclick={() => (cleanup = true)}>{t('Clean up…')}</button></div>
<div class="card">
  <div class="card-h">
    <input class="input" style="min-width:min(260px, 100%)" placeholder={t('Search name, IP, MAC, vendor, SSID…')} bind:value={q} aria-label={t('Search clients')} />
    {#if groups.length}<select class="input" style="width:auto" bind:value={grp} aria-label={t('Group')}><option value="">{t('All groups')}</option><option value="none">{t('No group')}</option>{#each groups as g}<option value={g}>{g}</option>{/each}</select>{/if}
    <div class="tabs" role="tablist">
      {#each kinds as [id, l]}
        <button role="tab" class:on={kind === id} aria-selected={kind === id} onclick={() => (kind = id)}>{l}</button>
      {/each}
    </div>
  </div>
  {#if picked.length}
    <div class="card-b" style="display:flex;gap:10px;align-items:center;border-bottom:1px solid var(--border)"><b>{t('{n} selected', { n: picked.length })}</b>
      <button class="btn sm primary" onclick={() => watchSelected(true)}>★ {t('Watch (alert when offline)')}</button>
      <button class="btn sm" onclick={() => watchSelected(false)}>{t('Stop watching')}</button>
      {#if groups.length}<select class="input" style="width:auto" aria-label={t('Set group')} onchange={e => { if (e.currentTarget.value) groupSelected(e.currentTarget.value === '-' ? '' : e.currentTarget.value); e.currentTarget.value = '' }}>
        <option value="">{t('Set group…')}</option>{#each groups as g}<option value={g}>{g}</option>{/each}<option value="-">{t('Remove from group')}</option></select>
        <button class="btn sm" onclick={() => groupSelected('', true)} title={t('Put each selected client into the group suggested for it (IoT, Servers)')}>{t('Apply suggested groups')}</button>{/if}
      <button class="btn sm" onclick={() => (sel = {})}>{t('Clear selection')}</button></div>
  {/if}
  <div class="scroll">
    <table>
      <thead><tr>
        <th style="width:28px"><input type="checkbox" aria-label={t('Select all shown')} checked={rows.length > 0 && rows.every(x => sel[x.c.mac])} onchange={e => { const n = {}; if (e.currentTarget.checked) rows.forEach(x => (n[x.c.mac] = true)); sel = n }} /></th>
        <th class="sortable" onclick={() => sort('name')}>{t('Client')}{arrow('name')}</th>
        <th class="sortable" onclick={() => sort('ip')}>{t('IP')}{arrow('ip')}</th>
        <th class="sortable" onclick={() => sort('device')}>{t('Connected to')}{arrow('device')}</th>
        <th class="sortable" onclick={() => sort('signal')}>{t('Signal')}{arrow('signal')}</th>
        <th class="sortable r" onclick={() => sort('live')}>{t('Now')}{arrow('live')}</th>
        <th class="sortable r" onclick={() => sort('usage')}>{t('Usage ({range})', { range: app.range === 'live' ? '1h' : app.range })}{arrow('usage')}</th>
        <th class="sortable r" onclick={() => sort('seen')}>{t('Last seen')}{arrow('seen')}</th></tr></thead>
      <tbody>
        {#each rows as x (x.c.mac)}
          <tr class="click" onclick={() => go('client/' + encodeURIComponent(x.c.mac))}>
            <td onclick={e => e.stopPropagation()}><input type="checkbox" aria-label={t('Select')} bind:checked={sel[x.c.mac]} /></td>
            <td><div style="display:flex;gap:10px;align-items:center"><Avatar client={x.c} online={x.c.online} />
              <div><b>{#if x.c.watch}<span title={t('Watched: alert when offline')}>★ </span>{/if}{display(x.c)}</b>{#if x.dns_bypass?.length} <span class="badge warn" title={t('Does not use the router’s DNS: {types}', { types: x.dns_bypass.map(tr).join(', ') })}>{t('DNS bypass')}</span>{/if}{#if x.c.group} <span class="badge">{x.c.group}</span>{:else if x.suggested_group && groups.includes(x.suggested_group)} <span class="muted" style="font-size:11.5px" title={t('Suggested group')}>({x.suggested_group}?)</span>{/if}<div class="muted mono" style="font-size:11.5px">{x.c.mac}{#if x.c.vendor} · {tr(x.c.vendor)}{/if}</div></div></div></td>
            <td class="mono">{x.c.ip || '—'}</td>
            <td>{#if x.c.device}{x.c.device}{#if x.c.wifi}<div><span class="badge acc">{x.c.ssid || t('Wi-Fi')}{x.c.band ? ' · ' + x.c.band : ''}</span></div>{:else}<div class="muted">{x.c.iface}</div>{/if}{:else}<span class="muted">—</span>{/if}</td>
            <td>{#if x.c.wifi && x.c.online}<span class="badge {signalQuality(x.c.signal).cls}">{signalQuality(x.c.signal).txt}</span>{#if health[x.c.mac]}{@const h = health[x.c.mac]} <span class="badge {h.poor ? 'bad' : 'ok'}" title={t('Wi-Fi score 24 h: signal −{a}, weak readings −{b}, roams −{c}, link rate −{d}', { a: h.signal_penalty, b: h.weak_penalty, c: h.roam_penalty, d: h.rate_penalty })}>{h.score}</span>{/if}{:else}<span class="muted">—</span>{/if}</td>
            <td class="r num">{x.live ? bps(x.live) : '—'}</td>
            <td class="r num">{bytes(x.bytes_up + x.bytes_down)}<div class="muted" style="font-size:11.5px">↓ {bytes(x.bytes_down)} ↑ {bytes(x.bytes_up)}</div></td>
            <td class="r muted">{x.c.online ? t('now') : ago(x.c.last_seen)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
    {#if !rows.length}{#if d.loading}<div class="empty">{t('Loading…')}</div>{:else if !(d.data || []).length}<Empty icon="laptop" title={t('No clients yet')} hint={t('Clients appear here as soon as your routers report them. Check Devices if nothing shows up.')} href="#/devices" action={t('Open Devices')} />{:else}<Empty compact icon="generic" title={t('No clients match')} hint={t('Try another search or filter.')} />{/if}{/if}
  </div>
</div>

{#if cleanup}<CleanupDialog onclose={() => (cleanup = false)} ondone={async () => { try { d.data = await api('/clients?range=' + (app.range === 'live' ? '1h' : app.range)) } catch {} }} />{/if}

<style>.head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 18px; }</style>
