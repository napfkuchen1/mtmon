<script>
  // Overview card "Internet": latency and loss measured by mtmon's own probes, plus reconnects of the WAN link.
  import { api } from './api.js'
  import { app, toast } from './state.svelte.js'
  import { poll } from './Poll.svelte.js'
  import { t, tr } from './i18n.svelte.js'
  import { ago } from './format.js'
  import Spark from './Spark.svelte'
  const rg = () => (app.range === 'live' ? '1h' : app.range)
  const d = poll(() => api('/internet?range=' + rg()), 30000)
  const x = $derived(d.data)
  const lat = $derived((x?.series || []).map(p => p.avg_ms))
  const loss = $derived((x?.series || []).map(p => p.loss_pct))
  const cls = (v, warn, bad) => (v >= bad ? 'bad' : v >= warn ? 'warn' : 'ok')
  async function toggle() {
    try { await api('/internet', { method: 'PUT', body: { enabled: !x.enabled } }); d.data = await api('/internet?range=' + rg()) } catch (e) { toast(tr(e.message)) }
  }
</script>

{#if x}
  <div class="card" style="margin-top:16px">
    <div class="card-h" data-default={x.enabled && x.now.loss_pct < 5 ? 'closed' : undefined} data-health={!x.enabled || !x.window.sent ? undefined : cls(x.now.loss_pct, 5, 20)}
      data-sum={x.enabled && x.window.sent ? (x.now.avg_ms ? x.now.avg_ms.toFixed(0) + ' ms' : '—') + ', ' + t('{p} % loss', { p: x.now.loss_pct.toFixed(0) }) : ''}><h2>{t('Internet')}</h2>
      <span class="muted" style="font-size:12px">{t('measured by mtmon: TCP connect to public resolvers every 30 s')}</span>
      <button class="sw" class:on={x.enabled} role="switch" aria-checked={x.enabled} aria-label={t('Internet probe')} onclick={toggle}></button></div>
    {#if !x.enabled}
      <div class="card-b muted">{t('The probe is off.')}</div>
    {:else if !x.window.sent}
      <div class="card-b muted">{t('No probes recorded yet.')}</div>
    {:else}
      <div class="card-b grid g4" style="align-items:start">
        <div><div class="l muted">{t('Latency now')}</div><div class="v num">{x.now.sent && x.now.avg_ms ? x.now.avg_ms.toFixed(0) + ' ms' : '—'}</div>
          <div class="l muted">{t('{ms} ms over the range', { ms: x.window.avg_ms.toFixed(0) })}</div><Spark points={lat} color="var(--accent)" /></div>
        <div><div class="l muted">{t('Packet loss')}</div><div class="v num"><span class="badge {cls(x.now.loss_pct, 5, 20)}">{x.now.loss_pct.toFixed(0)} %</span></div>
          <div class="l muted">{t('{p} % over the range · {d} % in 24 h', { p: x.window.loss_pct.toFixed(1), d: x.day.loss_pct.toFixed(1) })}</div><Spark points={loss} color="var(--bad)" /></div>
        <div><div class="l muted">{t('WAN reconnects (24 h)')}</div><div class="v num">{x.reconnects_24h}</div>
          <div class="l muted">{x.last_reconnect ? t('last reconnect {when}', { when: ago(x.last_reconnect) }) : t('none recorded')}</div></div>
        <div><div class="l muted">{t('Targets')}</div>
          {#each x.targets as g}<div class="num" style="font-size:12.5px"><span class="mono">{g.target}</span>
            {#if g.note}· <span class="muted">{tr(g.note)}</span>{:else}· {g.avg_ms ? g.avg_ms.toFixed(0) + ' ms' : '—'} · {g.loss_pct.toFixed(0)} %{#if g.err} · <span class="bad-t">{tr(g.err)}</span>{/if}{/if}</div>{/each}</div>
      </div>
    {/if}
  </div>
{/if}
<style>.bad-t { color: var(--bad); } .v { font-size: 22px; font-weight: 600; margin: 2px 0; } .l { font-size: 12px; }</style>
