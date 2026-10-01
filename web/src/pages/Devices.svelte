<script>
  import { api } from '../lib/api.js'
  import { app, go } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { dur, ago } from '../lib/format.js'
  import AddDevice from './AddDevice.svelte'
  const d = poll(() => api('/devices'), 5000)
  const liveOf = name => app.live.devices.find(x => x.name === name)
  let adding = $state(false)
  const flowState = x => !x.caps?.traffic_flow?.enabled && x.source === 'ui' && !x.flows_last ? null : x.flows_last ? (Date.now() / 1000 - x.flows_last < 120 ? 'ok' : 'stale') : 'wait'
</script>

<div class="head"><h1>Devices</h1><span class="muted">{d.data ? d.data.length : 0} managed via RouterOS REST</span>
  <span style="flex:1"></span><button class="btn primary" onclick={() => (adding = true)}>＋ Add device</button></div>
{#if d.error}<div class="warnbox">{d.error}</div>{/if}
{#if d.data && !d.data.length}
  <div class="card empty" style="padding:48px 16px">
    <h2 style="font-size:17px;margin-bottom:6px">Noch kein Gerät</h2>
    <p class="muted" style="max-width:520px;margin:0 auto 16px">Adresse + Admin-Zugang eingeben – mtmon erkennt Modell, WLAN, Firewall und Traffic Flow selbst, zeigt dir was es einrichten würde und baut es auf Wunsch wieder zurück.</p>
    <button class="btn primary" onclick={() => (adding = true)}>＋ Erstes Gerät hinzufügen</button>
  </div>
{/if}

<div class="grid g3">
  {#each d.data || [] as x (x.name)}
    {@const l = liveOf(x.name) || x.live}
    {@const fs = flowState(x)}
    <div class="card click" role="button" tabindex="0" onclick={() => go('device/' + encodeURIComponent(x.name))} onkeydown={e => e.key === 'Enter' && go('device/' + encodeURIComponent(x.name))}>
      <div class="card-h">
        <div class="t"><span class="dot" class:ok={l?.up} class:bad={!l?.up}></span><h2>{x.name}</h2></div>
        <span class="badge">{x.role}</span>
      </div>
      <div class="card-b">
        <div class="muted mono" style="margin-bottom:10px">{x.addr}{#if x.state?.model} · {x.state.model}{/if}</div>
        {#if l?.up}
          <div class="m"><span>CPU</span><div class="bar"><i style="width:{l.cpu}%;background:{l.cpu > 80 ? 'var(--bad)' : 'var(--accent)'}"></i></div><b class="num">{l.cpu.toFixed(0)}%</b></div>
          <div class="m"><span>RAM</span><div class="bar"><i style="width:{l.mem_pct}%"></i></div><b class="num">{l.mem_pct.toFixed(0)}%</b></div>
          <div class="foot muted"><span>Up {dur(l.uptime)}</span>{#if l.temp}<span>{l.temp.toFixed(0)} °C</span>{/if}{#if x.state?.version}<span>ROS {x.state.version.split(' ')[0]}</span>{/if}</div>
        {:else}
          <div class="badge bad" style="white-space:normal">{l?.err || x.state?.last_err || 'unreachable'}</div>
          <div class="muted" style="margin-top:8px">last seen {ago(x.state?.last_ok)}</div>
        {/if}
        <div class="chips">
          {#if x.caps?.wifi_stack === 'wifi'}<span class="badge acc">Wi-Fi</span>{/if}
          {#if fs === 'ok'}<span class="badge ok">Flows ✓</span>{:else if fs === 'wait'}<span class="badge warn">warte auf Flows …</span>{:else if fs === 'stale'}<span class="badge bad">Flows ausgeblieben ({ago(x.flows_last)})</span>{/if}
          {#if x.firewall_logging}<span class="badge ok">Firewall-Log</span>{/if}
          {#if x.managed}<span class="badge" title="mtmon hat Einstellungen am Router angelegt – Offboarding baut sie zurück">verwaltet</span>{:else if x.source === 'ui'}<span class="badge">nur lesend</span>{:else}<span class="badge">config.json</span>{/if}
        </div>
      </div>
    </div>
  {/each}
</div>
{#if adding}<AddDevice onclose={() => (adding = false)} />{/if}

<style>
  .head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 18px; }
  .click { cursor: pointer; } .click:hover { border-color: var(--border-2); }
  .t { display: flex; align-items: center; gap: 10px; }
  .m { display: grid; grid-template-columns: 38px 1fr 44px; gap: 10px; align-items: center; margin-bottom: 8px; }
  .m span { color: var(--muted); font-size: 12.5px; } .m b { text-align: right; font-weight: 600; }
  .foot { display: flex; gap: 14px; margin-top: 12px; font-size: 12.5px; flex-wrap: wrap; }
  .chips { display: flex; gap: 6px; flex-wrap: wrap; margin-top: 12px; }
</style>
