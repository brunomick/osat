import { get, writable } from 'svelte/store'
import { api, errorText, type NamedWorkspace } from './api'
import { tr } from './i18n'
import { confirmDialog, promptDialog } from './dialog'
import { untilUnlocked } from './lock'
import { installLayout, layout } from './layout'
import { closeTab, connections, openTab, settings, tabs, toast } from './stores'
import { restore, snapshot } from './workspace'

// Espacios de trabajo en la interfaz: restaurar la última sesión al abrir osat,
// guardarla mientras se trabaja y los workspaces con nombre de la barra
// lateral. Pasar de pestañas a datos y de vuelta está en workspace.ts.

/** Los workspaces con nombre, por nombre. */
export const namedWorkspaces = writable<NamedWorkspace[]>([])

export async function loadWorkspaces() {
  namedWorkspaces.set((await api.workspacesList()) ?? [])
}

/**
 * Abre las pestañas y el mosaico de un espacio guardado (las pestañas que haya
 * se conservan: quien llama las cierra antes si corresponde). Las pestañas no
 * conectan acá: cada una lo hace la primera vez que se muestra (deferConnect).
 * Avisa en una línea lo que no se pudo abrir. false si los datos no sirven.
 */
export function openWorkspace(data: unknown): boolean {
  const r = restore(data, get(connections))
  if (!r) {
    toast(tr('workspaces.unreadable'), 'error', 8000)
    return false
  }
  const idOf: Record<string, string> = {}
  for (const { ref, ...t } of r.tabs) idOf[ref] = openTab({ ...t, deferConnect: true })
  if (r.layout) installLayout(r.layout, idOf)
  const lost = [
    r.skipped.length && tr('workspaces.skipped', { names: r.skipped.join(', ') }),
    r.omitted?.length && tr(r.omitted.length === 1 ? 'workspaces.unsaved.one' : 'workspaces.unsaved.other', { names: r.omitted.join(', ') }),
  ].filter(Boolean)
  if (lost.length) toast(tr('workspaces.couldntOpen', { list: lost.join('; ') }), 'info', 8000)
  return true
}

/** Cierra todas las pestañas con las confirmaciones de siempre. false si el usuario dejó alguna abierta. */
export async function closeAllTabs(): Promise<boolean> {
  for (const { id } of get(tabs)) {
    await closeTab(id)
    if (get(tabs).some((t) => t.id === id)) return false
  }
  return true
}

// ---------- La última sesión ----------

let saveTimer: ReturnType<typeof setTimeout>
let lastSaved = ''

// Se guarda cuando algo cambia y se queda quieto un segundo (no hay un aviso confiable de que se cierra la ventana).
function saveLast() {
  const data = snapshot(get(tabs), get(layout))
  const json = JSON.stringify(data)
  if (json === lastSaved) return // cambió algo que no se guarda (el estado de una conexión, por ejemplo)
  api
    .workspaceLastSet(data)
    .then(() => (lastSaved = json))
    .catch(() => {}) // se reintenta con el próximo cambio
}

/**
 * Al abrir osat, con las conexiones y los ajustes ya leídos: si el ajuste está
 * activo, vuelven las pestañas de la última vez; después se empieza a guardar
 * la sesión. Nunca falla: si algo sale mal se avisa y se arranca vacío.
 * connectionsLoaded false: no se restaura ni se guarda (sin la lista de
 * conexiones, guardar pisaría la última sesión con una casi vacía).
 * Con osat bloqueado espera al desbloqueo: ni se abre ninguna conexión ni se guarda la sesión (todavía vacía).
 */
export async function startSession(connectionsLoaded: boolean) {
  if (!connectionsLoaded) return
  await untilUnlocked()
  if (get(settings).general.restoreSession && !get(tabs).length) {
    try {
      const last = await api.workspaceLastGet()
      if (last) openWorkspace(last)
    } catch (e) {
      toast(tr('workspaces.restoreFailed', { error: errorText(e) }), 'error', 8000)
    }
  }
  const schedule = () => {
    clearTimeout(saveTimer)
    saveTimer = setTimeout(saveLast, 1000)
  }
  tabs.subscribe(schedule)
  layout.subscribe(schedule)
}

// ---------- Workspaces con nombre (barra lateral) ----------

/** Lo abierto ahora, o null (con aviso) si no hay pestañas. */
function current() {
  if (!get(tabs).length) {
    toast(tr('workspaces.nothingToSave'))
    return null
  }
  return snapshot(get(tabs), get(layout))
}

/** Aviso al guardar: los editores con cambios sin guardar no entran. */
const omittedNote = (o?: string[]) => (o?.length ? tr(o.length === 1 ? 'workspaces.omittedNote.one' : 'workspaces.omittedNote.other', { names: o.join(', ') }) : '')

export async function saveCurrentWorkspace() {
  const data = current()
  if (!data) return
  const name = await promptDialog({ title: tr('workspaces.saveTitle'), message: tr('workspaces.nameLabel'), okText: tr('common.save') })
  if (!name) return
  try {
    const w = await api.workspaceSave('', name, data)
    await loadWorkspaces()
    toast(tr('workspaces.saved', { name: w.name, note: omittedNote(data.omitted) }), 'success', data.omitted ? 8000 : 4000)
  } catch (e) {
    toast(errorText(e), 'error')
  }
}

/** Reemplaza las pestañas abiertas por las del workspace, pidiendo confirmación si hay alguna. */
export async function openNamedWorkspace(w: NamedWorkspace) {
  const n = get(tabs).length
  if (n) {
    const ok = await confirmDialog({
      title: tr('workspaces.openTitle', { name: w.name }),
      message: tr(n === 1 ? 'workspaces.openMsg.one' : 'workspaces.openMsg.other', { count: n }),
      okText: tr('common.open'),
    })
    if (!ok || !(await closeAllTabs())) return
  }
  openWorkspace(w.data)
}

export async function updateWorkspace(w: NamedWorkspace) {
  const data = current()
  if (!data) return
  try {
    await api.workspaceSave(w.id, w.name, data)
    await loadWorkspaces()
    toast(tr('workspaces.updated', { name: w.name, note: omittedNote(data.omitted) }), 'success', data.omitted ? 8000 : 4000)
  } catch (e) {
    toast(errorText(e), 'error')
  }
}

export async function renameWorkspace(w: NamedWorkspace) {
  const name = await promptDialog({ title: tr('workspaces.renameTitle'), message: w.name, value: w.name, okText: tr('workspaces.rename') })
  if (!name || name === w.name) return
  try {
    await api.workspaceRename(w.id, name)
    await loadWorkspaces()
  } catch (e) {
    toast(errorText(e), 'error')
  }
}

export async function deleteWorkspace(w: NamedWorkspace) {
  const ok = await confirmDialog({
    title: tr('workspaces.deleteTitle'),
    message: tr('workspaces.deleteMsg', { name: w.name }),
    okText: tr('workspaces.delete'),
    danger: true,
  })
  if (!ok) return
  try {
    await api.workspaceDelete(w.id)
    await loadWorkspaces()
  } catch (e) {
    toast(errorText(e), 'error')
  }
}
