import { get, writable } from 'svelte/store'
import { api, errorText, type Connection, type TmuxList, type TmuxSession, type TmuxTarget } from './api'
import { connectWith } from './connect'
import { protocolOf } from './protocol'
import { connections, LOCAL, patchTab, tabs, toast, type Tab } from './stores'

// Shells persistentes: sesiones de tmux del servidor que siguen vivas aunque se
// cierre la pestaña (ver internal/remote/tmux.go). La barra lateral las lista
// debajo de cada servidor.

/** Mía (la creó este osat), de otro osat, o armada a mano con tmux. */
export type OwnerKind = 'mine' | 'other' | 'manual'

export function ownerKind(s: TmuxSession, me: string): OwnerKind {
  if (!s.owner) return 'manual'
  return s.owner === me ? 'mine' : 'other'
}

/** Gente adentro además de mí: los clientes de tmux menos mis pestañas conectadas a esa sesión. */
export function othersInside(s: TmuxSession, connId: string, list: readonly Pick<Tab, 'connId' | 'status' | 'tmux'>[]): number {
  const mine = list.filter((t) => t.connId === connId && t.status === 'ready' && t.tmux?.id === s.id).length
  return Math.max(0, s.attached - mine)
}

export const sortShells = (list: TmuxSession[]) => [...list].sort((a, b) => b.activity - a.activity)

/** Nombre que se propone al crear: el del servidor y un número libre. tmux cambia . y : por _. */
export function nextShellName(connName: string, list: TmuxSession[]): string {
  const base = connName.replace(/[.:]/g, '_')
  const used = new Set(list.map((s) => s.name))
  let n = 1
  while (used.has(`${base} ${n}`)) n++
  return `${base} ${n}`
}

/** Lo que se sabe de las shells de cada servidor (por id de conexión). */
export interface ShellsState {
  list?: TmuxList
  /** El servidor no tiene tmux (o es muy viejo): no se muestra nada. */
  unavailable?: string
  /** No hay conexión abierta: la lista es la última conocida. */
  stale?: boolean
  loading?: boolean
}
export const shellsByConn = writable<Record<string, ShellsState>>({})

const patch = (connId: string, p: ShellsState) => shellsByConn.update((m) => ({ ...m, [connId]: { ...m[connId], ...p } }))

/** Una pestaña conectada de ese servidor por la que hablar con tmux (terminal, SFTP o puertos). */
export function liveSession(connId: string): string | undefined {
  return get(tabs).find((t) => t.connId === connId && t.connId !== LOCAL && t.status === 'ready' && t.sessionId && ['terminal', 'sftp', 'ports'].includes(t.kind))?.sessionId
}

/**
 * Actualiza la lista de un servidor. Usa una pestaña conectada; si no hay y
 * `dial`, conecta, lista y cierra (puede preguntar la contraseña). Sin `dial`,
 * la lista queda como estaba, marcada como vieja.
 */
export async function refreshShells(c: Connection, dial = false): Promise<void> {
  const sid = liveSession(c.id)
  if (!sid && !dial) {
    if (get(shellsByConn)[c.id]?.list) patch(c.id, { stale: true })
    return
  }
  patch(c.id, { loading: true })
  try {
    const list = sid ? await api.tmuxSessions(sid) : await connectWith(c, {}, (req) => api.tmuxSessionsDial(req))
    patch(c.id, { list, stale: !sid, unavailable: undefined, loading: false })
  } catch (e) {
    const msg = errorText(e)
    // Sin tmux o con uno viejo no hay nada que mostrar; los demás errores (red) dejan la lista como estaba.
    if (/tmux/i.test(msg)) patch(c.id, { unavailable: msg, list: undefined, loading: false })
    else patch(c.id, { loading: false, stale: true })
    if (dial) toast(msg, 'error')
  }
}

/** Actualiza la de los servidores que tienen alguna pestaña conectada. */
export function refreshConnected() {
  const ids = new Set(get(tabs).filter((t) => t.status === 'ready' && t.connId !== LOCAL).map((t) => t.connId))
  for (const c of get(connections)) if (ids.has(c.id) && protocolOf(c) === 'ssh') refreshShells(c)
}

/** La pestaña para abrir una shell persistente (reusa la conexión de otra pestaña del servidor si hay). */
export function shellTab(c: Connection, s: TmuxSession, readOnly = false): Omit<Tab, 'id' | 'status'> {
  const tmux: TmuxTarget = { id: s.id, name: s.name, ...(readOnly ? { readOnly } : {}), ...(s.windows > 1 || s.panes > 1 ? { plain: true } : {}) }
  return { kind: 'terminal', connId: c.id, title: c.name, label: s.name, color: c.color, tmux, fromSession: liveSession(c.id) }
}

/**
 * Renombra una terminal desde la pestaña o la barra lateral. En una shell
 * persistente renombra la sesión de tmux (todos ven el nombre nuevo); vacío no
 * vale ahí. En las demás, el nombre va en `label` (vacío vuelve al original).
 */
export async function renameTerminal(t: Tab, name: string): Promise<void> {
  const n = name.trim()
  if (!t.tmux) {
    patchTab(t.id, { label: n || undefined })
    return
  }
  if (!n || n === t.tmux.name || !t.sessionId) return
  try {
    const got = await api.tmuxRename(t.sessionId, t.tmux.id, n)
    patchTab(t.id, { label: got, tmux: { ...t.tmux, name: got } })
    const c = get(connections).find((x) => x.id === t.connId)
    if (c) refreshShells(c)
  } catch (e) {
    toast(errorText(e), 'error')
  }
}
