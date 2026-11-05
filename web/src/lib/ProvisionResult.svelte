<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script>
  import { t, tr } from './i18n.svelte.js'

  // The outcome of a router change: the cause of a failure first, then what succeeded, then what the undo did.
  let { steps = [], rollback = [], error = '' } = $props()
  const failed = $derived((steps || []).filter((s) => !s.ok))
  const done = $derived((steps || []).filter((s) => s.ok))
</script>

{#each failed as s}<div class="pr-row"><span class="badge bad">✗</span> <b>{tr(s.title)}</b>{#if s.msg}<div class="pr-cause">{tr(s.msg)}</div>{/if}</div>{/each}
{#if error && !failed.length}<div class="pr-row"><b>{tr(error)}</b></div>{/if}
{#each done as s}<div class="pr-row"><span class="badge ok">✓</span> {tr(s.title)}</div>{/each}
{#if rollback?.length}
  <div class="pr-row pr-head">{t('Undo:')}</div>
  {#each rollback as s}
    <div class="pr-row"><span class="badge {s.ok ? 'ok' : 'bad'}">{s.ok ? '✓' : '✗'}</span> {tr(s.title)}
      {#if s.msg}<div class="pr-cause">{tr(s.msg)}</div>{/if}
      {#if s.cmd}<div class="muted">{t('Fix by hand in the RouterOS terminal:')}</div><pre class="code pr-cmd">{s.cmd}</pre>{/if}
    </div>
  {/each}
{/if}

<style>
  .pr-row { font-size: 12.5px; margin-top: 2px; }
  .pr-head { font-weight: 600; margin-top: 8px; }
  .pr-cause { margin: 2px 0 2px 4px; overflow-wrap: anywhere; }
  .pr-cmd { margin: 4px 0; white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
