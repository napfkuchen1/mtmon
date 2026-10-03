<script>
  import { ICONS } from './icons.js'
  import { deviceKind } from './format.js'
  // kind: explicit icon name; otherwise guessed from the client (label / hostname / vendor)
  let { client = null, kind = '', size = 18, color = 'currentColor' } = $props()
  const k = $derived(kind || (client ? deviceKind(client) : 'generic'))
</script>
<svg class="dicon" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d={ICONS[k] || ICONS.generic} /></svg>
<style>.dicon { flex: none; vertical-align: -3px; }</style>
