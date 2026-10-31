<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script>
  // Overview: how the client groups are doing (online count and traffic of the last 24 h).
  import { api } from './api.js'
  import { poll } from './Poll.svelte.js'
  import { t } from './i18n.svelte.js'
  import { bytes } from './format.js'
  const gl = poll(() => api('/groups'), 60000)
  const groups = $derived((gl.data?.groups || []).filter(g => g.clients > 0))
  const max = $derived(groups.reduce((m, g) => Math.max(m, g.up + g.down), 0) || 1)
</script>
{#if groups.length}
  <div class="card" style="margin-top:16px"><div class="card-h" data-default="closed" data-health={groups.some(g => g.online < g.clients) ? 'warn' : 'ok'} data-sum={t('{n} groups', { n: groups.length }) + ' · ' + t('{on}/{all} online', { on: groups.reduce((s, g) => s + g.online, 0), all: groups.reduce((s, g) => s + g.clients, 0) })}><h2>{t('Groups')}</h2><a href="#/clients">{t('All →')}</a></div>
    <div class="list">{#each groups as g (g.name)}
      <div class="row"><div class="top"><div class="name"><b>{g.name}</b> <span class="sub">{t('{on}/{all} online', { on: g.online, all: g.clients })}</span></div><div class="val num">{bytes(g.up + g.down)}</div></div>
        <div class="bar"><i style="width:{((g.up + g.down) / max) * 100}%"></i></div></div>{/each}</div>
  </div>
{/if}
<style>
  .row { padding: 10px 16px; border-bottom: 1px solid var(--border); } .row:last-child { border-bottom: 0; }
  .top { display: flex; justify-content: space-between; gap: 12px; margin-bottom: 6px; } .sub { color: var(--muted); font-size: 12px; }
  .bar { height: 4px; background: var(--card-2); border-radius: 2px; } .bar i { display: block; height: 100%; background: var(--accent); border-radius: 2px; }
</style>
