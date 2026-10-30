// Collapsible cards: every `.card` whose `.card-h` has an <h2> folds when the title is clicked. No markup is injected
// (Svelte owns the DOM); the chevron comes from CSS and the state is kept per page in localStorage.
const ROUTES = new Set(['overview', 'device', 'client', 'internet', 'firewall', 'suggestions'])
const KEY = 'mtmon.collapsed.'

// Stored per page: { c: titles the viewer folded, o: titles the viewer opened although they start folded }.
// Cards whose header says data-default="closed" start folded until the viewer opens them.
function load(route) {
  try {
    const v = JSON.parse(localStorage.getItem(KEY + route) || '[]')
    return Array.isArray(v) ? { c: new Set(v), o: new Set() } : { c: new Set(v.c || []), o: new Set(v.o || []) }
  } catch { return { c: new Set(), o: new Set() } }
}
function save(route, st) { try { localStorage.setItem(KEY + route, JSON.stringify({ c: [...st.c], o: [...st.o] })) } catch {} }
const isClosed = (h, st) => {
  const t = titleOf(h)
  return st.c.has(t) || (h.closest('.card-h')?.dataset.default === 'closed' && !st.o.has(t))
}

const titleOf = h => (h.textContent || '').trim().slice(0, 80)
const heads = root => [...root.querySelectorAll('.card > .card-h h2')].filter(h => titleOf(h))

function paint(h, closed) {
  const card = h.closest('.card')
  card.classList.toggle('folded', closed)
  h.setAttribute('aria-expanded', closed ? 'false' : 'true')
}

// collapsible(node, route): decorate the cards below node now and whenever Svelte renders new ones.
export function collapsible(node, route) {
  let r = route
  const wire = () => {
    if (!ROUTES.has(r)) return
    const closed = load(r)
    for (const h of heads(node)) {
      if (h.dataset.fold !== r) {
        h.dataset.fold = r
        h.classList.add('foldable')
        h.setAttribute('role', 'button')
        h.tabIndex = 0
        paint(h, isClosed(h, closed))
      }
    }
  }
  const toggle = h => {
    const st = load(r), t = titleOf(h), now = !isClosed(h, st)
    if (now) { st.c.add(t); st.o.delete(t) } else { st.c.delete(t); st.o.add(t) }
    save(r, st)
    paint(h, now)
  }
  const click = e => {
    const h = e.target.closest?.('h2.foldable')
    if (h && node.contains(h) && !e.target.closest('a,button,input,select')) toggle(h)
  }
  const key = e => {
    const h = e.target.closest?.('h2.foldable')
    if (h && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); toggle(h) }
  }
  const all = e => {
    const st = { c: new Set(), o: new Set() }
    for (const h of heads(node)) (e.detail === 'close' ? st.c : st.o).add(titleOf(h))
    save(r, st)
    for (const h of heads(node)) paint(h, isClosed(h, st))
  }
  const mo = new MutationObserver(wire)
  mo.observe(node, { childList: true, subtree: true })
  node.addEventListener('click', click)
  node.addEventListener('keydown', key)
  window.addEventListener('mtmon:fold', all)
  wire()
  return {
    update(next) { r = next; for (const h of node.querySelectorAll('h2.foldable')) { delete h.dataset.fold; h.classList.remove('foldable') } wire() },
    destroy() { mo.disconnect(); node.removeEventListener('click', click); node.removeEventListener('keydown', key); window.removeEventListener('mtmon:fold', all) },
  }
}

export const foldable = route => ROUTES.has(route)
export const foldAll = close => window.dispatchEvent(new CustomEvent('mtmon:fold', { detail: close ? 'close' : 'open' }))
