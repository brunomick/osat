import { get, writable } from 'svelte/store'
import type { Tab } from './stores'

// Broadcast: lo que se escribe o pega en una de las terminales elegidas va a todas.
// Acá está solo la lógica (qué terminales participan y a cuáles va cada cosa);
// las vistas la usan.

/** Lo que el broadcast necesita saber de una pestaña. */
export type BroadcastTab = Pick<Tab, 'id' | 'kind' | 'status' | 'sessionId'>

export interface Broadcast {
  /** Ids de las pestañas que participan. Vacío = apagado; si está prendido hay al menos dos. */
  targets: string[]
}

export const broadcast = writable<Broadcast>({ targets: [] })

/** Prendido (dos o más terminales). */
export const isActive = () => get(broadcast).targets.length >= 2

/** Arranca con estas pestañas (sin repetir). Con menos de dos no arranca y lo que hubiera queda apagado. */
export function start(ids: string[]) {
  const targets = [...new Set(ids)]
  broadcast.set({ targets: targets.length >= 2 ? targets : [] })
}

export function stop() {
  broadcast.set({ targets: [] })
}

/** Saca una terminal (se cerró o se cayó). Si quedan menos de dos, se detiene. */
export function remove(id: string) {
  const { targets } = get(broadcast)
  if (!targets.includes(id)) return
  const rest = targets.filter((t) => t !== id)
  broadcast.set({ targets: rest.length >= 2 ? rest : [] })
}

/** Saca las que ya no son una terminal conectada (cerradas, caídas, reconectando). */
export function prune(tabs: BroadcastTab[]) {
  const { targets } = get(broadcast)
  if (!targets.length) return
  const ok = new Set(candidates(tabs).map((t) => t.id))
  const rest = targets.filter((t) => ok.has(t))
  if (rest.length === targets.length) return
  broadcast.set({ targets: rest.length >= 2 ? rest : [] })
}

/** Las terminales que se pueden elegir: abiertas y conectadas. Las de SFTP, RDP y demás no. */
export function candidates<T extends BroadcastTab>(tabs: T[]): (T & { sessionId: string })[] {
  return tabs.filter((t): t is T & { sessionId: string } => t.kind === 'terminal' && t.status === 'ready' && !!t.sessionId)
}

/** Las que vienen marcadas al abrir el diálogo: las que se ven en el mosaico (la activa de cada panel). */
export function defaultSelection(cands: BroadcastTab[], visibleIds: string[]): string[] {
  return cands.filter((t) => visibleIds.includes(t.id)).map((t) => t.id)
}

/**
 * A qué sesiones va lo que se escribe en la pestaña `fromTabId`: las de todas
 * las terminales del broadcast que están conectadas (incluida la propia). Vacío
 * si esa pestaña no participa o no hay broadcast.
 */
export function recipients(fromTabId: string, tabs: BroadcastTab[]): { id: string; sessionId: string }[] {
  const { targets } = get(broadcast)
  return targets.includes(fromTabId) ? destinations(tabs) : []
}

/** Las sesiones conectadas de todas las terminales del broadcast, participe o no quien pregunta. */
export function destinations(tabs: BroadcastTab[]): { id: string; sessionId: string }[] {
  const { targets } = get(broadcast)
  const ready = new Map(candidates(tabs).map((t) => [t.id, t.sessionId]))
  return targets.filter((id) => ready.has(id)).map((id) => ({ id, sessionId: ready.get(id)! }))
}

/**
 * Pestañas cuya shell activó el pegado entre corchetes. Cada terminal publica el suyo
 * (setBracketed) porque puede cambiar de una a otra: otra shell, un programa a pantalla completa.
 * Las que no figuran no lo tienen.
 */
export const bracketedModes = writable<Record<string, boolean>>({})

export function setBracketed(tabId: string, on: boolean) {
  bracketedModes.update((m) => {
    if (!!m[tabId] === on) return m
    const { [tabId]: _, ...rest } = m
    return on ? { ...rest, [tabId]: true } : rest
  })
}

/** Lo que se escribe en la sesión de cada destino: el pegado con el modo de esa terminal, no el de quien lo manda. */
export function pasteEach(
  dests: { id: string; sessionId: string }[],
  text: string,
  enter: boolean,
  modes: Record<string, boolean>,
): { sessionId: string; data: string }[] {
  return dests.map((d) => ({ sessionId: d.sessionId, data: pasteData(text, enter, !!modes[d.id]) }))
}

/**
 * Los bytes de pegar `text` (y mandarlo con Enter si `enter`) directo a una
 * sesión, como lo haría xterm: saltos de línea como Enter y, si la shell activó
 * el pegado entre corchetes, envuelto en sus marcas.
 */
export function pasteData(text: string, enter: boolean, bracketed: boolean): string {
  const body = text.replace(/\r?\n/g, '\r')
  return (bracketed ? `\x1b[200~${body}\x1b[201~` : body) + (enter ? '\r' : '')
}
