import type { Connection } from './api'
import type { LayoutNode, PlainLayout } from './layout'
import { tr } from './i18n'
import { legacyTitle, tabTitle } from './tabtitle'
import { LOCAL, type Tab, type TabKind } from './stores'

// Espacios de trabajo: las pestañas abiertas y cómo están repartidas en el
// mosaico, para guardarlas y volver a abrirlas (la última sesión y los
// workspaces con nombre). Acá está solo la lógica de pasar de las pestañas y el
// mosaico a datos guardables y de vuelta; abrirlas es cosa de la interfaz.
//
// Nunca se guardan contraseñas, sesiones ni el contenido de las terminales:
// solo lo necesario para abrir de nuevo cada pestaña.

/** Una pestaña guardada. `ref` la nombra dentro de este espacio (los ids de pestaña no sirven entre sesiones). */
export interface SavedTab {
  ref: string
  kind: TabKind
  connId: string
  title: string
  color?: string
  shell?: string
  initialDir?: string
  cwd?: string
  path?: string
  url?: string
}

export interface WorkspaceData {
  version: 1
  tabs: SavedTab[]
  /** El mosaico con `ref` en lugar de ids de pestaña; null si no hay pestañas. */
  layout: PlainLayout | null
  /** Títulos de los editores que no se guardaron por tener cambios sin guardar (solo si hay), para avisarlo al abrir. */
  omitted?: string[]
}

/** Una pestaña lista para abrir con openTab, más la referencia con la que figura en el mosaico. */
export type RestoredTab = Omit<Tab, 'id' | 'status'> & { ref: string }

export interface Restored {
  tabs: RestoredTab[]
  layout: PlainLayout | null
  /** Títulos de las pestañas que no se pueden abrir (su conexión ya no existe). */
  skipped: string[]
  /** Títulos de los editores que no se guardaron porque tenían cambios sin guardar (solo si hay). */
  omitted?: string[]
}

const KINDS: TabKind[] = ['terminal', 'sftp', 'editor', 'rdp', 'vnc', 'local', 'repo', 'browser', 'site', 'ports']
const MAX_DEPTH = 32

/** Una pestaña que se puede volver a abrir: un editor con cambios sin guardar no, y el editor y el repositorio necesitan su ruta (y el sitio web, su dirección). */
function recreatable(t: Tab): boolean {
  if (!KINDS.includes(t.kind)) return false // las de osat (ajustes, importar…) no son de un espacio de trabajo
  if (t.kind === 'editor' && t.dirty) return false
  if ((t.kind === 'editor' || t.kind === 'repo') && !t.path) return false
  if (t.kind === 'site' && !t.url) return false
  return true
}

/**
 * Saca del árbol las referencias que no están en `keep` y los paneles y divisiones que quedan vacíos.
 * Cada referencia queda una sola vez (en su primer lugar); `keep` se va vaciando.
 */
function prunePlain(n: PlainLayout, keep: Set<string>): PlainLayout | null {
  if (n.kind === 'pane') {
    const tabs = [...new Set(n.tabs.filter((r) => keep.has(r)))]
    tabs.forEach((r) => keep.delete(r))
    if (!tabs.length) return null
    return { kind: 'pane', tabs, active: n.active && tabs.includes(n.active) ? n.active : tabs[0] }
  }
  const a = prunePlain(n.a, keep)
  const b = prunePlain(n.b, keep)
  if (!a || !b) return a ?? b
  return { ...n, a, b }
}

/** Arma lo que se guarda a partir de las pestañas y el mosaico de ahora. */
export function snapshot(tabs: Tab[], layout: LayoutNode): WorkspaceData {
  const byId = new Map(tabs.map((t) => [t.id, t]))
  const refOf = new Map<string, string>()
  const saved: SavedTab[] = []
  const omitted: string[] = []

  // Las referencias siguen el orden del mosaico, así lo guardado no depende de cuándo se abrió cada pestaña.
  const take = (n: LayoutNode) => {
    if (n.kind === 'split') {
      take(n.a)
      take(n.b)
      return
    }
    for (const id of n.tabs) {
      const t = byId.get(id)
      if (!t || refOf.has(id)) continue
      if (!recreatable(t)) {
        if (t.kind === 'editor' && t.dirty) omitted.push(t.title)
        continue
      }
      const ref = `t${saved.length + 1}`
      refOf.set(id, ref)
      const s: SavedTab = { ref, kind: t.kind, connId: t.connId, title: t.title }
      for (const k of ['color', 'shell', 'initialDir', 'cwd', 'path', 'url'] as const) {
        const v = t[k]
        if (v) s[k] = v
      }
      saved.push(s)
    }
  }
  take(layout)

  const plain = (n: LayoutNode): PlainLayout =>
    n.kind === 'pane'
      ? { kind: 'pane', tabs: n.tabs.flatMap((id) => refOf.get(id) ?? []), active: (n.active && refOf.get(n.active)) || null }
      : { kind: 'split', dir: n.dir, ratio: n.ratio, a: plain(n.a), b: plain(n.b) }
  const data: WorkspaceData = { version: 1, tabs: saved, layout: saved.length ? prunePlain(plain(layout), new Set(refOf.values())) : null }
  if (omitted.length) data.omitted = omitted
  return data
}

// ---------- Leer lo guardado ----------
// Lo guardado puede venir de otra versión o estar dañado: se lee con cuidado y lo que no sirve se descarta.

const isObj = (x: unknown): x is Record<string, unknown> => typeof x === 'object' && x !== null && !Array.isArray(x)
const str = (x: unknown): string | undefined => (typeof x === 'string' && x !== '' ? x : undefined)

function readTab(x: unknown): SavedTab | null {
  if (!isObj(x)) return null
  const { ref, kind, connId, title } = x
  if (typeof ref !== 'string' || !ref || !KINDS.includes(kind as TabKind) || typeof connId !== 'string' || typeof title !== 'string') return null
  const t: SavedTab = { ref, kind: kind as TabKind, connId, title: legacyTitle(kind as TabKind, title) }
  for (const k of ['color', 'shell', 'initialDir', 'cwd', 'path', 'url'] as const) {
    const v = str(x[k])
    if (v) t[k] = v
  }
  return t
}

function readLayout(x: unknown, depth = 0): PlainLayout | null {
  if (!isObj(x) || depth > MAX_DEPTH) return null
  if (x.kind === 'pane') {
    if (!Array.isArray(x.tabs)) return null
    return { kind: 'pane', tabs: x.tabs.filter((r): r is string => typeof r === 'string'), active: str(x.active) ?? null }
  }
  if (x.kind === 'split') {
    const a = readLayout(x.a, depth + 1)
    const b = readLayout(x.b, depth + 1)
    if (!a || !b) return a ?? b // una mitad dañada: queda la otra
    return { kind: 'split', dir: x.dir === 'col' ? 'col' : 'row', ratio: typeof x.ratio === 'number' ? x.ratio : 0.5, a, b }
  }
  return null
}

/** Dónde arranca una pestaña guardada: el SFTP y "Este equipo" vuelven a la carpeta en la que estaban. */
function startDir(t: SavedTab): string | undefined {
  if (t.kind === 'sftp') return t.cwd ?? t.initialDir
  if (t.kind === 'local') return t.cwd === 'Este equipo' ? '::drives' : (t.cwd ?? t.initialDir) // igual que al duplicar la pestaña
  return t.initialDir
}

/**
 * Lo guardado, listo para abrir: las pestañas a crear, el mosaico y los títulos
 * de las que no se pueden abrir porque su conexión ya no existe. null si no es
 * un espacio de trabajo que esta versión entienda.
 */
export function restore(data: unknown, connections: Connection[]): Restored | null {
  if (!isObj(data) || data.version !== 1 || !Array.isArray(data.tabs)) return null
  const omitted = Array.isArray(data.omitted) ? data.omitted.filter((x): x is string => typeof x === 'string' && x !== '') : []
  const withOmitted = (r: Restored): Restored => (omitted.length ? { ...r, omitted } : r)
  const layout = readLayout(data.layout)
  if (!layout) return withOmitted({ tabs: [], layout: null, skipped: [] })

  const known = new Set(connections.map((c) => c.id))
  const inLayout = new Set<string>()
  const collect = (n: PlainLayout) => {
    if (n.kind === 'pane') n.tabs.forEach((r) => inLayout.add(r))
    else (collect(n.a), collect(n.b))
  }
  collect(layout)

  const tabs: RestoredTab[] = []
  const skipped: string[] = []
  const seen = new Set<string>()
  for (const raw of data.tabs) {
    const t = readTab(raw)
    if (!t || seen.has(t.ref) || !inLayout.has(t.ref)) continue // sin lugar en el mosaico no se abre
    seen.add(t.ref)
    if (t.kind === 'site' && !t.url) continue // sin dirección no hay qué abrir
    // Un sitio web no es de ningún servidor.
    if (t.kind !== 'site' && t.connId !== LOCAL && !known.has(t.connId)) {
      skipped.push(tabTitle(tr, t))
      continue
    }
    tabs.push({
      ref: t.ref,
      kind: t.kind,
      connId: t.connId,
      title: t.title,
      color: t.color,
      shell: t.shell,
      initialDir: startDir(t),
      path: t.path,
      url: t.url,
    })
  }
  return withOmitted({ tabs, layout: prunePlain(layout, new Set(tabs.map((t) => t.ref))), skipped })
}

// ---------- Conexión diferida ----------

/** Lo que necesita `mountable` de una pestaña. */
export type DeferTab = Pick<Tab, 'id' | 'kind' | 'connId' | 'status' | 'sessionId' | 'deferConnect'>

/**
 * De las pestañas restauradas que esperan para conectar (deferConnect), las que
 * ya se pueden montar: las que se ven ahora (`visible`). Una de un servidor que
 * otra pestaña ya tiene conectado reusa esa conexión (fromSession), así la
 * contraseña se pide una sola vez. Mientras otra pestaña del mismo servidor hace
 * su primera conexión (`starting`), espera; y de las que se muestran juntas del
 * mismo servidor, conecta una sola. Las de esta PC, las de escritorio remoto (RDP y VNC) y
 * las de sitios web no comparten conexión: se montan enseguida.
 */
export function mountable(tabs: DeferTab[], visible: Set<string>, starting: Set<string>): { id: string; fromSession?: string }[] {
  const shares = (t: DeferTab) => t.connId !== LOCAL && t.kind !== 'rdp' && t.kind !== 'vnc' && t.kind !== 'site'
  const live = (t: DeferTab) => !t.deferConnect && shares(t)
  const busy = new Set(tabs.filter((t) => live(t) && starting.has(t.id) && t.status === 'connecting').map((t) => t.connId))
  const out: { id: string; fromSession?: string }[] = []
  for (const t of tabs) {
    if (!t.deferConnect || !visible.has(t.id)) continue
    if (!shares(t)) {
      out.push({ id: t.id })
      continue
    }
    const src = tabs.find((x) => x.id !== t.id && x.connId === t.connId && live(x) && x.status === 'ready' && x.sessionId)
    if (src) {
      out.push({ id: t.id, fromSession: src.sessionId })
      continue
    }
    if (busy.has(t.connId)) continue
    busy.add(t.connId)
    out.push({ id: t.id })
  }
  return out
}
