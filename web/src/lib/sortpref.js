// The sort a user picked for a table is kept in the browser, one entry per table (mtmon.sort.<table>).
export function loadSort(table, def) {
  try {
    const v = JSON.parse(localStorage.getItem('mtmon.sort.' + table))
    if (v && typeof v.key === 'string' && typeof v.desc === 'boolean') return v
  } catch {}
  return def
}
export function saveSort(table, key, desc) {
  try { localStorage.setItem('mtmon.sort.' + table, JSON.stringify({ key, desc })) } catch {}
}

// Keeps a list in the order it was last ranked: values refresh on every poll, but rows are re-ranked only every
// `every` ms or when `ctx` changes, so nothing jumps under the cursor. New rows are appended, vanished ones dropped.
export function stableOrder(prev, next, idOf, { ctx, every = 30000, now = Date.now() }) {
  if (!prev || prev.ctx !== ctx || now - prev.at >= every) return { ctx, at: now, rows: next }
  const byId = new Map(next.map(r => [idOf(r), r]))
  const kept = prev.rows.map(r => byId.get(idOf(r))).filter(Boolean)
  const seen = new Set(kept.map(idOf))
  return { ctx, at: prev.at, rows: [...kept, ...next.filter(r => !seen.has(idOf(r)))] }
}
