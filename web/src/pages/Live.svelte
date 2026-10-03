<script>
  import Flag from '../lib/Flag.svelte'
  import { app, go, setPaused } from '../lib/state.svelte.js'
  import { bps, bytes, clock, protoName } from '../lib/format.js'
  import { t, tr } from '../lib/i18n.svelte.js'
  import Chart from '../lib/Chart.svelte'

  let filter = $state('')
  const series = $derived([
    { name: t('Download'), color: 'var(--down)', points: app.history.map(h => ({ x: h.t, y: h.down })) },
    { name: t('Upload'), color: 'var(--up)', points: app.history.map(h => ({ x: h.t, y: h.up })) }
  ])
  const feed = $derived(app.feed.filter(c => !filter || (c.name + c.rip + c.host + c.svc + c.rport).toLowerCase().includes(filter.toLowerCase())))
  const maxRate = $derived(app.live.clients.reduce((m, c) => Math.max(m, c.up_bps + c.down_bps), 0) || 1)
  const arrow = { u: '↑', d: '↓', i: '⇄' }
</script>

<div class="head"><h1>{t('Live')}</h1>
  <span class="badge" class:ok={app.live.connected}><span class="dot" class:ok={app.live.connected}></span>{app.live.connected ? t('streaming') : t('disconnected')}</span>
  <span class="muted">{t('rates are a 30 s average of exported flows')}</span></div>

<div class="grid g3" style="grid-template-columns: 2fr 1fr">
  <div class="card">
    <div class="card-h"><h2>{t('Total throughput')}</h2>
      <div class="legend"><span><i style="background:var(--down)"></i>↓ {bps(app.live.down_bps)}</span><span><i style="background:var(--up)"></i>↑ {bps(app.live.up_bps)}</span></div></div>
    <div class="card-b"><Chart {series} fmt={bps} height={200} empty={t('Waiting for live data…')} /></div>
  </div>
  <div class="card">
    <div class="card-h"><h2>{t('Active clients')}</h2></div>
    {#if !app.live.clients.length}<div class="empty">{t('No active clients')}</div>{/if}
    {#each app.live.clients.slice(0, 8) as c (c.key)}
      <div class="crow click" role="button" tabindex="0" onclick={() => go('client/' + encodeURIComponent(c.key))} onkeydown={e => e.key === 'Enter' && go('client/' + encodeURIComponent(c.key))}>
        <div class="t"><span class="nm">{c.name}</span><span class="num muted">{bps(c.up_bps + c.down_bps)}</span></div>
        <div class="bar"><i style="width:{((c.up_bps + c.down_bps) / maxRate) * 100}%"></i></div>
      </div>
    {/each}
  </div>
</div>

<div class="card" style="margin-top:16px">
  <div class="card-h"><h2>{t('Connections')}</h2>
    <div style="display:flex;flex-wrap:wrap;gap:8px;align-items:center">
      <input class="input" placeholder={t('Filter client, host, port, service…')} bind:value={filter} aria-label={t('Filter connections')} />
      <button class="btn" onclick={() => setPaused(!app.paused)}>{app.paused ? t('Resume') : t('Pause')}</button>
      <button class="btn" onclick={() => (app.feed = [])}>{t('Clear')}</button>
    </div></div>
  <div class="scroll">
    <table>
      <thead><tr><th>{t('Time')}</th><th>{t('Client')}</th><th></th><th>{t('Destination')}</th><th>{t('Port')}</th><th>{t('Service')}</th><th>{t('Country')}</th><th class="r">{t('Bytes')}</th></tr></thead>
      <tbody>
        {#each feed.slice(0, 120) as c (c.seq)}
          <tr>
            <td class="mono muted">{clock(c.ts)}</td>
            <td><a href="#/client/{encodeURIComponent(c.client)}">{c.name}</a></td>
            <td class:dn={c.dir === 'd'} class:upc={c.dir === 'u'}>{arrow[c.dir]}</td>
            <td><span class="mono">{c.rip}</span>{#if c.host}<span class="muted"> · {c.host}</span>{/if}</td>
            <td class="mono">{protoName(c.proto)}/{c.rport}</td>
            <td>{#if c.svc}<span class="badge">{tr(c.svc)}</span>{/if}</td>
            <td><Flag cc={c.cc} />{c.cc}</td>
            <td class="r num">{bytes(c.bytes)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
    {#if !feed.length}<div class="empty">{app.paused ? t('Paused') : t('Waiting for flows… (MikroTik exports active flows every 30 s)')}</div>{/if}
  </div>
</div>

<style>
  .head { display: flex; align-items: center; gap: 14px; margin-bottom: 18px; }
  .crow { padding: 9px 16px; border-bottom: 1px solid var(--border); } .crow:last-child { border: 0; }
  .click { cursor: pointer; } .click:hover { background: var(--card-2); }
  .t { display: flex; justify-content: space-between; margin-bottom: 5px; gap: 10px; }
  .nm { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
  .dn { color: var(--down); font-weight: 700; } .upc { color: var(--up); font-weight: 700; }
  @media (max-width: 1000px) { .grid { grid-template-columns: 1fr !important; } }
</style>
