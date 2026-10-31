<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script>
  // Settings: DNS-bypass overview (period, active/ended, "mark as fixed") and the alert switch (off by default).
  import { api } from './api.js'
  import { toast } from './state.svelte.js'
  import { poll } from './Poll.svelte.js'
  import { ago } from './format.js'
  import { t, tr } from './i18n.svelte.js'
  let range = $state('7d')
  const d = poll(() => api('/dns-bypass?range=' + range), 30000)
  async function reload() { d.data = await api('/dns-bypass?range=' + range) }
  async function toggle() {
    try { await api('/dns-bypass', { method: 'PUT', body: { alert: !d.data.alert } }); await reload(); toast(t('Saved')) } catch (e) { toast(tr(e.message)) }
  }
  async function fixed(body) {
    try { await api('/dns-bypass/reset', { method: 'POST', body }); await reload(); toast(t('Marked as fixed')) } catch (e) { toast(tr(e.message)) }
  }
  const ranges = [['1h', '1 hour'], ['24h', '24 hours'], ['7d', '7 days'], ['30d', '30 days']]
</script>
{#if d.data}
  <div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('DNS bypass')}</h2>
    <span class="muted">{t('{n} clients do not use the router’s DNS', { n: d.data.total })}</span>
    <select bind:value={range} onchange={reload} aria-label={t('Period')}>{#each ranges as [v, l]}<option value={v}>{t(l)}</option>{/each}</select>
    <button class="sw" class:on={d.data.alert} role="switch" aria-checked={d.data.alert} aria-label={t('Alert when a client starts bypassing the local DNS')} onclick={toggle}></button></div>
    <div class="card-b muted" style="font-size:13px">{t('Alert (info) when a client starts to use DNS-over-TLS/QUIC/HTTPS or a public DNS server directly. Off by default; switching it on records the current state first, so existing clients do not raise alerts.')}
      {#if d.data.total}<a href="#/clients/dns-bypass">{t('Show clients')}</a>{/if}</div>
    {#if d.data.clients.length || d.data.observing.length}
      <div class="card-b">
        <div class="scroll"><table><tbody>
          {#each d.data.clients as c (c.mac)}
            <tr><td><a href={'#/clients/' + c.mac}>{c.name}</a></td><td>{c.types.map(tr).join(' · ')}{#if c.redirected} <span class="badge" title={t('The router redirects these DNS queries to itself, so the client gets the router’s answers.')}>{t('redirected – harmless')}</span>{/if}</td>
              <td class="muted">{#if c.active}{t('active')}{:else}{t('ended (last {when})', { when: ago(c.last) })}{/if}</td>
              <td class="r"><button class="btn sm" onclick={() => fixed({ mac: c.mac })}>{t('Mark as fixed')}</button></td></tr>
          {/each}
          {#each d.data.observing as o (o.mac)}
            <tr><td><a href={'#/clients/' + o.mac}>{o.name}</a></td><td class="muted" colspan="3">{t('under observation: no bypass since {when}', { when: ago(o.since) })}</td></tr>
          {/each}
        </tbody></table></div>
        {#if d.data.clients.length > 1}<p style="margin-bottom:0"><button class="btn sm" onclick={() => fixed({ all: true })}>{t('Mark all as fixed')}</button></p>{/if}
        <p class="muted" style="font-size:12.5px;margin-bottom:0">{t('“Mark as fixed” ignores older traffic. If the client bypasses the local DNS again it is flagged again (and alerted, if the alert is on) – this can take up to an hour. A bypass counts as ended after {h} h without traffic.', { h: d.data.ended_after_h })}</p>
      </div>
    {/if}
  </div>
{/if}
