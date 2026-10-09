<script>
  import Avatar from '../lib/Avatar.svelte'
  import Empty from '../lib/Empty.svelte'
  import { api, download } from '../lib/api.js'
  import { app, go } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { t, tr } from '../lib/i18n.svelte.js'
  import { bps, bytes, ago, display, signalQuality } from '../lib/format.js'
  import CleanupDialog from '../lib/CleanupDialog.svelte'
  let cleanup = $state(false)

  let q = $state(''), kind = $state('all'), sortKey = $state('live'), desc = $state(true)
  const d = poll(() => api('/clients?range=' + (app.range === 'live' ? '1h' : app.range)), 5000)

  const rows = $derived.by(() => {
    const live = new Map(app.live.clients.map(c => [c.key, c]))
    let r = (d.data || []).map(x => ({ ...x, c: x.client, live: (live.get(x.client.mac)?.up_bps || 0) + (live.get(x.client.mac)?.down_bps || 0) || x.up_bps + x.down_bps }))
    const s = q.trim().toLowerCase()
    if (s) r = r.filter(x => [x.c.mac, x.c.ip, x.c.hostname, x.c.label, x.c.vendor, x.c.device, x.c.ssid].join(' ').toLowerCase().includes(s))
    if (kind === 'online') r = r.filter(x => x.c.online)
    if (kind === 'wifi') r = r.filter(x => x.c.wifi && x.c.online)
    if (kind === 'wired') r = r.filter(x => !x.c.wifi && x.c.online)
    if (kind === 'offline') r = r.filter(x => !x.c.online)
    const val = x => ({ name: display(x.c).toLowerCase(), ip: x.c.ip.split('.').map(n => n.padStart(3, '0')).join('.'), live: x.live, usage: x.bytes_up + x.bytes_down, signal: x.c.signal || -999, seen: x.c.last_seen, device: x.c.device }[sortKey])
    r.sort((a, b) => { const A = val(a), B = val(b); return (A < B ? -1 : A > B ? 1 : 0) * (desc ? -1 : 1) })
    return r
  })
  function sort(k) { if (sortKey === k) desc = !desc; else { sortKey = k; desc = k !== 'name' && k !== 'ip' } }
  const arrow = k => sortKey === k ? (desc ? ' ↓' : ' ↑') : ''
  const kinds = $derived([['all', t('All')], ['online', t('Online')], ['wifi', t('Wi-Fi')], ['wired', t('Wired')], ['offline', t('Offline')]])
  const counts = $derived({ all: d.data?.length || 0, online: (d.data || []).filter(x => x.client.online).length })
</script>

<div class="head"><h1>{t('Clients')}</h1><span class="muted">{t('{online} online · {all} known', { online: counts.online, all: counts.all })}</span>
  <span style="flex:1"></span><a class="btn" href="#/inbox">{t('New devices')}</a> <button class="btn" onclick={() => (cleanup = true)}>{t('Clean up…')}</button></div>
<div class="card">
  <div class="card-h">
    <input class="input" style="min-width:260px" placeholder={t('Search name, IP, MAC, vendor, SSID…')} bind:value={q} aria-label={t('Search clients')} />
    <div class="tabs" role="tablist">
      {#each kinds as [id, l]}
        <button role="tab" class:on={kind === id} aria-selected={kind === id} onclick={() => (kind = id)}>{l}</button>
      {/each}
    </div>
  </div>
  <div class="scroll">
    <table>
      <thead><tr>
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
            <td><div style="display:flex;gap:10px;align-items:center"><Avatar client={x.c} online={x.c.online} />
              <div><b>{display(x.c)}</b><div class="muted mono" style="font-size:11.5px">{x.c.mac}{#if x.c.vendor} · {tr(x.c.vendor)}{/if}</div></div></div></td>
            <td class="mono">{x.c.ip || '—'}</td>
            <td>{#if x.c.device}{x.c.device}{#if x.c.wifi}<div><span class="badge acc">{x.c.ssid || t('Wi-Fi')}{x.c.band ? ' · ' + x.c.band : ''}</span></div>{:else}<div class="muted">{x.c.iface}</div>{/if}{:else}<span class="muted">—</span>{/if}</td>
            <td>{#if x.c.wifi && x.c.online}<span class="badge {signalQuality(x.c.signal).cls}">{signalQuality(x.c.signal).txt}</span>{:else}<span class="muted">—</span>{/if}</td>
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
