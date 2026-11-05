<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script>
  // LTE signal on the Internet page (#300). Rendered only when a device reports an LTE interface.
  import { t } from './i18n.svelte.js'
  import { ago } from './format.js'

  let { items = [] } = $props()
  const num = (v, unit) => (v === null || v === undefined ? '—' : Math.round(v) + ' ' + unit)
</script>

{#if items.length}
  <div class="card" style="margin-bottom:16px">
    <div class="card-h"><h2>{t('LTE signal')}</h2><span class="muted" style="font-size:12.5px">{t('read every 5 minutes')}</span></div>
    <div class="scroll"><table>
      <thead><tr><th>{t('Interface')}</th><th class="r">RSRP</th><th class="r">RSRQ</th><th class="r">SINR</th><th class="r">RSSI</th><th class="r">{t('Updated')}</th></tr></thead>
      <tbody>
        {#each items as l (l.device + '/' + l.iface)}
          <tr><td><b>{l.device}</b> · {l.iface} <span class="muted">{l.tech || ''}</span></td><td class="r num">{num(l.rsrp, 'dBm')}</td><td class="r num">{num(l.rsrq, 'dB')}</td>
            <td class="r num">{num(l.sinr, 'dB')}</td><td class="r num">{num(l.rssi, 'dBm')}</td><td class="r muted">{l.ts ? ago(l.ts) : '—'}</td></tr>
        {/each}
      </tbody></table></div>
  </div>
{/if}
