<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script>
  // "…" overflow menu for destructive actions: they stay out of the primary button row (#280)
  import { t } from './i18n.svelte.js'
  let { children } = $props()
  let box = $state(null)
  const close = () => { if (box) box.open = false }
</script>

<details class="more" bind:this={box} onclick={e => e.stopPropagation()} onfocusout={e => { if (!e.currentTarget.contains(e.relatedTarget)) close() }}>
  <summary class="btn sm" aria-label={t('More actions')} title={t('More actions')}>…</summary>
  <div class="panel" role="menu" onclick={close}>{@render children()}</div>
</details>

<style>
  .more { position: relative; display: inline-block; }
  .more > summary { list-style: none; cursor: pointer; user-select: none; }
  .more > summary::-webkit-details-marker { display: none; }
  .panel { position: absolute; right: 0; top: calc(100% + 4px); z-index: 20; min-width: 190px; padding: 6px; display: flex; flex-direction: column; gap: 4px;
    background: var(--card); border: 1px solid var(--border-2); border-radius: 10px; box-shadow: 0 8px 24px rgba(0, 0, 0, .18); }
  .panel :global(.btn) { width: 100%; justify-content: flex-start; color: var(--bad); }
</style>
