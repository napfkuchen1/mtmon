<script>
  import { app, checkSession, parseHash, go, openLive, closeLive, applyTheme, setTheme, setRange } from './lib/state.svelte.js'
  import { api } from './lib/api.js'
  import Login from './pages/Login.svelte'
  import Overview from './pages/Overview.svelte'
  import Live from './pages/Live.svelte'
  import Devices from './pages/Devices.svelte'
  import DeviceDetail from './pages/DeviceDetail.svelte'
  import Clients from './pages/Clients.svelte'
  import ClientDetail from './pages/ClientDetail.svelte'
  import Insights from './pages/Insights.svelte'
  import Services from './pages/Services.svelte'
  import Firewall from './pages/Firewall.svelte'
  import Topology from './pages/Topology.svelte'
  import Alerts from './pages/Alerts.svelte'
  import Settings from './pages/Settings.svelte'

  applyTheme()
  parseHash()
  window.addEventListener('hashchange', () => { parseHash(); window.scrollTo(0, 0) })
  checkSession()

  $effect(() => { if (app.user) openLive(); else if (app.user === false) closeLive() })

  const nav = [
    ['overview', 'Overview', 'M3 12l9-8 9 8M5 10v10h5v-6h4v6h5V10'],
    ['live', 'Live', 'M3 12h4l3-8 4 16 3-8h4'],
    ['devices', 'Devices', 'M4 6h16v5H4zM4 13h16v5H4zM7 8.5h.01M7 15.5h.01'],
    ['clients', 'Clients', 'M16 11a4 4 0 10-8 0 4 4 0 008 0zM4 21c0-4 4-6 8-6s8 2 8 6'],
    ['services', 'Services', 'M4 6h7v5H4zM13 6h7v5h-7zM4 13h7v5H4zM13 13h7v5h-7z'],
    ['firewall', 'Firewall', 'M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6zM9 12l2 2 4-4'],
    ['insights', 'Insights', 'M4 20V10M10 20V4M16 20v-7M22 20H2'],
    ['topology', 'Topology', 'M12 5a2 2 0 100-4 2 2 0 000 4zM5 21a2 2 0 100-4 2 2 0 000 4zM19 21a2 2 0 100-4 2 2 0 000 4zM12 5v6M12 11l-7 6M12 11l7 6'],
    ['alerts', 'Alerts', 'M6 9a6 6 0 1112 0c0 6 3 7 3 8H3c0-1 3-2 3-8zM10 21h4'],
    ['settings', 'Settings', 'M12 15a3 3 0 100-6 3 3 0 000 6zM19 12a7 7 0 00-.1-1.2l2-1.5-2-3.4-2.3 1a7 7 0 00-2-1.2L14 3h-4l-.6 2.7a7 7 0 00-2 1.2l-2.3-1-2 3.4 2 1.5a7 7 0 000 2.4l-2 1.5 2 3.4 2.3-1a7 7 0 002 1.2L10 21h4l.6-2.7a7 7 0 002-1.2l2.3 1 2-3.4-2-1.5c.1-.4.1-.8.1-1.2z']
  ]
  const ranges = [['live', 'Live'], ['1h', '1 h'], ['24h', '24 h'], ['7d', '7 d'], ['30d', '30 d']]
  const showRange = $derived(['overview', 'clients', 'client', 'insights', 'device', 'services', 'firewall'].includes(app.route.name))
  const active = $derived(app.route.name === 'client' ? 'clients' : app.route.name === 'device' ? 'devices' : app.route.name)
  let openAlerts = $state(0)
  $effect(() => {
    if (!app.user) return
    let t, stop = false
    const run = async () => { try { openAlerts = (await api('/alerts')).filter(a => !a.acked && a.severity !== 'info').length } catch {} if (!stop) t = setTimeout(run, 15000) }
    run()
    return () => { stop = true; clearTimeout(t) }
  })
  async function logout() { try { await api('/logout', { method: 'POST' }) } catch {} app.user = false }
</script>

{#if app.user === null}
  <div class="boot"><span class="dot pulse"></span></div>
{:else if app.user === false}
  <Login />
{:else}
  <div class="shell">
    <aside>
      <a class="brand" href="#/overview">
        <svg width="26" height="26" viewBox="0 0 32 32" aria-hidden="true"><rect width="32" height="32" rx="7" fill="var(--accent)"/><path d="M6 21l5-7 4 4 5-9 6 12" stroke="#fff" stroke-width="2.6" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>
        <span>mtmon</span>
      </a>
      <nav>
        {#each nav as [id, label, d]}
          <a href="#/{id}" class:on={active === id}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path {d} /></svg>
            {label}
            {#if id === 'alerts' && openAlerts}<span class="cnt">{openAlerts}</span>{/if}
          </a>
        {/each}
      </nav>
      <div class="foot">
        <div class="conn"><span class="dot" class:ok={app.live.connected} class:bad={!app.live.connected}></span>{app.live.connected ? 'Live connected' : 'Reconnecting…'}</div>
        <div class="row2">
          <select class="input sm" aria-label="Theme" value={app.theme} onchange={e => setTheme(e.target.value)}>
            <option value="auto">Auto</option><option value="light">Light</option><option value="dark">Dark</option>
          </select>
          <button class="btn sm" onclick={logout}>Sign out</button>
        </div>
      </div>
    </aside>
    <main>
      <header>
        <div class="mob">
          <select class="input" aria-label="Navigate" value={active} onchange={e => go(e.target.value)}>
            {#each nav as [id, label]}<option value={id}>{label}</option>{/each}
          </select>
        </div>
        <div class="spacer"></div>
        {#if showRange}
          <div class="tabs" role="tablist" aria-label="Time range">
            {#each ranges as [id, label]}
              <button role="tab" aria-selected={app.range === id} class:on={app.range === id} onclick={() => setRange(id)}>{label}</button>
            {/each}
          </div>
        {/if}
      </header>
      <div class="page">
        {#if app.route.name === 'overview'}<Overview />
        {:else if app.route.name === 'live'}<Live />
        {:else if app.route.name === 'devices'}<Devices />
        {:else if app.route.name === 'device'}<DeviceDetail />
        {:else if app.route.name === 'clients'}<Clients />
        {:else if app.route.name === 'client'}<ClientDetail />
        {:else if app.route.name === 'insights'}<Insights />
        {:else if app.route.name === 'services'}<Services />
        {:else if app.route.name === 'firewall'}<Firewall />
        {:else if app.route.name === 'topology'}<Topology />
        {:else if app.route.name === 'alerts'}<Alerts />
        {:else if app.route.name === 'settings'}<Settings />
        {:else}<div class="empty">Page not found. <a href="#/overview">Go to Overview</a></div>{/if}
      </div>
    </main>
  </div>
{/if}
{#if app.toast}<div class="toast" role="status">{app.toast}</div>{/if}

<style>
  .boot { height: 100vh; display: grid; place-items: center; }
  .shell { display: grid; grid-template-columns: var(--sidebar) 1fr; min-height: 100vh; }
  aside { position: sticky; top: 0; height: 100vh; background: var(--card); border-right: 1px solid var(--border); display: flex; flex-direction: column; padding: 14px 10px; }
  .brand { display: flex; align-items: center; gap: 10px; font-weight: 700; font-size: 17px; color: var(--text); padding: 6px 10px 16px; letter-spacing: -.01em; }
  .brand:hover { text-decoration: none; }
  nav { display: flex; flex-direction: column; gap: 2px; }
  nav a { display: flex; align-items: center; gap: 11px; padding: 8px 10px; border-radius: 8px; color: var(--muted); font-weight: 500; }
  nav a:hover { background: var(--card-2); color: var(--text); text-decoration: none; }
  nav a.on { background: var(--accent-bg); color: var(--accent-strong); }
  .cnt { margin-left: auto; background: var(--bad); color: #fff; border-radius: 999px; font-size: 11px; padding: 0 7px; font-weight: 600; }
  .foot { margin-top: auto; display: flex; flex-direction: column; gap: 10px; padding: 8px 6px 0; border-top: 1px solid var(--border); }
  .conn { display: flex; align-items: center; gap: 8px; color: var(--muted); font-size: 12.5px; }
  .row2 { display: flex; gap: 8px; } .row2 select { flex: 1; padding: 4px 6px; }
  main { min-width: 0; }
  header { display: flex; align-items: center; gap: 12px; padding: 12px 28px; border-bottom: 1px solid var(--border); background: var(--card); position: sticky; top: 0; z-index: 5; min-height: 57px; }
  .spacer { flex: 1; }
  .mob { display: none; }
  .page { padding: 24px 28px 48px; max-width: 1500px; }
  @media (max-width: 860px) {
    .shell { grid-template-columns: 1fr; } aside { display: none; } .mob { display: block; }
    header, .page { padding-left: 16px; padding-right: 16px; }
  }
</style>
