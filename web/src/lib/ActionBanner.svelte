<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script>
  // "Action needed" (#319): one-time admin steps from release notes, found by checks (GET /api/actions, admin only).
  // The viewer role gets 403 and sees nothing; an item vanishes by itself once its check passes.
  import { api } from './api.js'
  import { go } from './state.svelte.js'
  import { poll } from './Poll.svelte.js'
  import { t, tr } from './i18n.svelte.js'

  let { only = '' } = $props()
  const a = poll(() => api('/actions'), 60000)
  const items = $derived((a.data?.items || []).filter(i => !only || i.link.includes(only)))
</script>

{#if items.length}
  <div class="warnbox" style="margin-bottom:12px">
    <b>{t('Action needed')}</b>
    {#each items as i (i.id)}
      <div style="margin-top:6px"><b>{tr(i.title)}</b> – {tr(i.detail)} <a href={'#' + i.link} onclick={e => { e.preventDefault(); go(i.link.slice(1)) }}>{t('Open')}</a></div>
    {/each}
  </div>
{/if}
