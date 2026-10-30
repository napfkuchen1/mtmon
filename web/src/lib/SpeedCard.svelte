<script>
  // Overview tile: the latest speed-test result and the trend of the last 7 days; links to the Internet page.
  import { api } from './api.js'
  import { poll } from './Poll.svelte.js'
  import { t } from './i18n.svelte.js'
  import { bps, ago } from './format.js'
  import Spark from './Spark.svelte'
  const d = poll(() => api('/internet/speedtests?range=7d'), 60000)
  const x = $derived(d.data)
  const ok = $derived((x?.items || []).filter(i => !i.error))
  const down = $derived(ok.filter(i => i.down_bps > 0).map(i => i.down_bps).reverse())
  const lastUp = $derived(ok.find(i => i.up_bps > 0))
  const contract = $derived((x?.settings?.contract_down_mbps || 0) * 1e6)
  const pct = $derived(contract && x?.last?.down_bps ? Math.round((x.last.down_bps / contract) * 100) : 0)
</script>

{#if x && x.last}
  <a class="card sc" href="#/internet" style="margin-top:16px; display:block; color:inherit; text-decoration:none">
    <div class="card-h" data-default="closed" data-sum={bps(x.last.down_bps) + ' ↓ · ' + (lastUp ? bps(lastUp.up_bps) : '—') + ' ↑'}><h2>{t('Speed')}</h2><span class="muted" style="font-size:12px">{t('last test {when}', { when: ago(x.last.ts) })}</span></div>
    <div class="card-b grid g4" style="align-items:start">
      <div><div class="l muted">{t('Download')}</div><div class="v num">{bps(x.last.down_bps)}</div>
        {#if pct}<div class="l muted">{t('{p} % of the contracted speed', { p: pct })}</div>{/if}<Spark points={down} color="var(--down)" /></div>
      <div><div class="l muted">{t('Upload')}</div><div class="v num">{lastUp ? bps(lastUp.up_bps) : '—'}</div></div>
      <div><div class="l muted">{t('Latency')}</div><div class="v num">{x.last.ping_ms ? x.last.ping_ms.toFixed(0) + ' ms' : '—'}</div></div>
      <div><div class="l muted">{t('Tests (7 d)')}</div><div class="v num">{x.summary.tests}</div>
        <div class="l muted">{t('{f} failed', { f: x.summary.failed })}</div></div>
    </div>
  </a>
{/if}
<style>.v { font-size: 22px; font-weight: 600; margin: 2px 0; } .l { font-size: 12px; } .sc:hover { border-color: var(--border-2); }</style>
