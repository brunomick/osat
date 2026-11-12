import { get, writable } from 'svelte/store'

// Layout en mosaico del área principal: un árbol binario cuyas hojas son
// paneles (cada uno con su barra de pestañas) y cuyos nodos internos son
// divisiones horizontales o verticales. Las pestañas viven en el store `tabs`;
// acá solo se guarda en qué panel está cada una y cuál muestra cada panel.

export interface PaneNode {
  kind: 'pane'
  id: string
  tabs: string[] // ids de pestañas, en el orden de su barra
  active: string | null // pestaña visible en el panel
}

export interface SplitNode {
  kind: 'split'
  id: string
  dir: 'row' | 'col' // row: a | b (lado a lado); col: a sobre b
  ratio: number // fracción del espacio para a
  a: LayoutNode
  b: LayoutNode
}

export type LayoutNode = PaneNode | SplitNode
export type Side = 'left' | 'right' | 'top' | 'bottom'

let seq = 0
const newPane = (tabs: string[] = []): PaneNode => ({ kind: 'pane', id: `pane-${++seq}`, tabs, active: tabs[0] ?? null })

const first = newPane()
export const layout = writable<LayoutNode>(first)
export const focusedPane = writable<string>(first.id)

/** Rectángulo del cuerpo de cada panel, relativo al contenedor de vistas.
 * Lo publica cada panel; las vistas (que no se remontan al mover pestañas)
 * se posicionan encima. */
export const paneRects = writable<Record<string, { x: number; y: number; w: number; h: number }>>({})

export function panes(n: LayoutNode): PaneNode[] {
  return n.kind === 'pane' ? [n] : [...panes(n.a), ...panes(n.b)]
}
export const findPane = (n: LayoutNode, id: string) => panes(n).find((p) => p.id === id)
export const paneOfTab = (n: LayoutNode, tabId: string) => panes(n).find((p) => p.tabs.includes(tabId))

function mapPanes(n: LayoutNode, f: (p: PaneNode) => LayoutNode): LayoutNode {
  return n.kind === 'pane' ? f(n) : { ...n, a: mapPanes(n.a, f), b: mapPanes(n.b, f) }
}

// Saca los paneles vacíos y colapsa las divisiones que quedan con un solo hijo.
function prune(n: LayoutNode): LayoutNode | null {
  if (n.kind === 'pane') return n.tabs.length ? n : null
  const a = prune(n.a)
  const b = prune(n.b)
  if (!a || !b) return a ?? b
  return { ...n, a, b }
}

function commit(n: LayoutNode) {
  const root = prune(n) ?? newPane()
  layout.set(root)
  const ps = panes(root)
  if (!ps.some((p) => p.id === get(focusedPane))) focusedPane.set(ps[0].id)
}

function without(p: PaneNode, tabId: string): PaneNode {
  if (!p.tabs.includes(tabId)) return p
  const i = p.tabs.indexOf(tabId)
  const tabs = p.tabs.filter((t) => t !== tabId)
  const active = p.active === tabId ? (tabs[i] ?? tabs[i - 1] ?? null) : p.active
  return { ...p, tabs, active }
}

/** Agrega una pestaña al panel enfocado (o al indicado), después de la activa. */
export function addTab(tabId: string, paneId = get(focusedPane)) {
  const l = get(layout)
  const target = findPane(l, paneId) ?? panes(l)[0]
  commit(
    mapPanes(l, (p) => {
      if (p.id !== target.id) return p
      const i = p.active ? p.tabs.indexOf(p.active) + 1 : p.tabs.length
      return { ...p, tabs: [...p.tabs.slice(0, i), tabId, ...p.tabs.slice(i)], active: tabId }
    }),
  )
  focusedPane.set(target.id)
}

/** Saca una pestaña de su panel (y el panel, si queda vacío). */
export function removeTab(tabId: string) {
  commit(mapPanes(get(layout), (p) => without(p, tabId)))
}

/** Muestra la pestaña en su panel y enfoca ese panel. */
export function activate(tabId: string) {
  const l = get(layout)
  const p = paneOfTab(l, tabId)
  if (!p) return
  if (p.active !== tabId) commit(mapPanes(l, (x) => (x.id === p.id ? { ...x, active: tabId } : x)))
  focusedPane.set(p.id)
}

/** Mueve una pestaña a otro panel (o la reordena en el suyo). index = posición en la barra destino. */
export function moveTab(tabId: string, toPane: string, index?: number) {
  const l = get(layout)
  if (!findPane(l, toPane)) return
  commit(
    mapPanes(l, (p) => {
      if (p.id !== toPane) return without(p, tabId)
      let tabs = p.tabs.filter((t) => t !== tabId)
      // index se calculó con la barra original: si la pestaña estaba antes, se corre uno.
      let i = index ?? tabs.length
      const old = p.tabs.indexOf(tabId)
      if (old >= 0 && old < i) i--
      i = Math.max(0, Math.min(tabs.length, i))
      tabs = [...tabs.slice(0, i), tabId, ...tabs.slice(i)]
      return { ...p, tabs, active: tabId }
    }),
  )
  focusedPane.set(toPane)
}

/** Divide el panel paneId y pone la pestaña en el panel nuevo, del lado indicado. */
export function splitWith(tabId: string, paneId: string, side: Side) {
  const l = get(layout)
  const src = paneOfTab(l, tabId)
  if (!src || (src.id === paneId && src.tabs.length === 1)) return // quedaría un panel vacío
  const fresh = newPane([tabId])
  const dir = side === 'left' || side === 'right' ? 'row' : 'col'
  const after = side === 'right' || side === 'bottom'
  commit(
    mapPanes(l, (p) => {
      const q = without(p, tabId)
      if (p.id !== paneId) return q
      return { kind: 'split', id: `split-${++seq}`, dir, ratio: 0.5, a: after ? q : fresh, b: after ? fresh : q }
    }),
  )
  focusedPane.set(fresh.id)
}

export function setRatio(splitId: string, ratio: number) {
  const r = Math.min(0.9, Math.max(0.1, ratio))
  const walk = (n: LayoutNode): LayoutNode =>
    n.kind === 'pane' ? n : n.id === splitId ? { ...n, ratio: r } : { ...n, a: walk(n.a), b: walk(n.b) }
  layout.set(walk(get(layout)))
}

/** Un mosaico sin ids de panel: lo que se guarda en un espacio de trabajo. Las pestañas se nombran con una referencia propia. */
export type PlainLayout =
  | { kind: 'pane'; tabs: string[]; active: string | null }
  | { kind: 'split'; dir: 'row' | 'col'; ratio: number; a: PlainLayout; b: PlainLayout }

/**
 * Reemplaza todo el mosaico por `tree` de una sola vez. `idOf` traduce la
 * referencia de cada pestaña al id de la pestaña ya creada; las referencias sin
 * id se descartan, y los paneles que quedan vacíos con ellas. Los paneles y las
 * divisiones reciben ids nuevos y se enfoca el primer panel. Las pestañas tienen
 * que existir en el store `tabs` (quien llama las cierra o las crea).
 */
export function installLayout(tree: PlainLayout, idOf: Record<string, string>) {
  const build = (n: PlainLayout): LayoutNode => {
    if (n.kind === 'pane') {
      const tabs = [...new Set(n.tabs.map((r) => idOf[r]).filter((id): id is string => !!id))]
      const active = n.active ? idOf[n.active] : undefined
      const pane = newPane(tabs)
      if (active && tabs.includes(active)) pane.active = active
      return pane
    }
    const ratio = Number.isFinite(n.ratio) ? Math.min(0.9, Math.max(0.1, n.ratio)) : 0.5
    return { kind: 'split', id: `split-${++seq}`, dir: n.dir === 'col' ? 'col' : 'row', ratio, a: build(n.a), b: build(n.b) }
  }
  commit(build(tree))
  focusedPane.set(panes(get(layout))[0].id)
}

/** Pestañas del panel enfocado (para Ctrl+Tab y Alt+1…9). */
export function focusedTabs(): string[] {
  return findPane(get(layout), get(focusedPane))?.tabs ?? []
}

// ---------- Arrastrar pestañas ----------

export type DropTarget =
  | { paneId: string; zone: 'center' | Side } // sobre el cuerpo de un panel
  | { paneId: string; bar: true; index: number } // sobre la barra de pestañas de un panel

export const tabDrag = writable<{ tabId: string; x: number; y: number; target: DropTarget | null } | null>(null)

/** Zona de un panel según dónde esté el cursor: bordes (25%) dividen, centro mueve. */
export function zoneAt(r: { x: number; y: number; w: number; h: number }, x: number, y: number): 'center' | Side {
  const fx = (x - r.x) / r.w
  const fy = (y - r.y) / r.h
  const edge = Math.min(fx, 1 - fx, fy, 1 - fy)
  if (edge > 0.25) return 'center'
  if (edge === fx) return 'left'
  if (edge === 1 - fx) return 'right'
  return edge === fy ? 'top' : 'bottom'
}

export function applyDrop(tabId: string, t: DropTarget) {
  if ('bar' in t) moveTab(tabId, t.paneId, t.index)
  else if (t.zone === 'center') moveTab(tabId, t.paneId)
  else splitWith(tabId, t.paneId, t.zone)
}
