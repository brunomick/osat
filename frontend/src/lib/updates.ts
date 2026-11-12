import { get, writable } from 'svelte/store'
import { api, errorText, on, type UpdateInfo } from './api'
import { locale, tr } from './i18n'
import { activeTabId, openTab, tabs, toast } from './stores'

// Actualizaciones (app_update.go): el aviso de versión nueva, el progreso mientras se instala,
// y un error si falla (UpdateDialog). Las novedades de la primera vez que se abre una versión nueva van en una pestaña (WhatsNewView).

export type UpdateUi =
  | { kind: 'available'; info: UpdateInfo }
  | { kind: 'progress'; info: UpdateInfo; done: number; total: number; restarting: boolean }
  | { kind: 'error'; info: UpdateInfo; error: string }

export const updateUi = writable<UpdateUi | null>(null)

/** Lo que trae la versión instalada (markdown), para la pestaña de novedades. */
export const whatsNewNotes = writable('')

/** Busca una versión nueva. manual: lo pidió el usuario (avisa también si no hay o si falla). */
export async function checkForUpdates(manual: boolean) {
  try {
    const info = await api.updateCheck(get(locale))
    if (info.available) updateUi.set({ kind: 'available', info })
    else if (manual) toast(tr('update.upToDate', { version: info.current }), 'success')
  } catch (e) {
    if (manual) toast(tr('update.checkError', { error: errorText(e) }), 'error', 6000)
  }
}

/** Baja e instala la versión nueva; si sale bien, osat se cierra y se vuelve a abrir. */
export async function installUpdate(info: UpdateInfo) {
  updateUi.set({ kind: 'progress', info, done: 0, total: 0, restarting: false })
  const off = on('update:progress', (p: { done: number; total: number }) =>
    updateUi.update((u) => (u?.kind === 'progress' ? { ...u, done: p.done, total: p.total } : u)),
  )
  try {
    await api.updateApply()
    updateUi.update((u) => (u?.kind === 'progress' ? { ...u, restarting: true } : u))
  } catch (e) {
    updateUi.set({ kind: 'error', info, error: errorText(e) })
  } finally {
    off()
  }
}

/** Muestra las novedades si es la primera vez que se abre esta versión. Devuelve si las mostró. */
export async function showWhatsNew(): Promise<boolean> {
  const w = await api.whatsNew(get(locale)).catch(() => null)
  if (!w?.version) return false
  whatsNewNotes.set(w.notes)
  const existing = get(tabs).find((t) => t.kind === 'whatsnew')
  if (existing) activeTabId.set(existing.id)
  else openTab({ kind: 'whatsnew', connId: '', title: w.version }) // el título se arma al mostrarlo (lib/tabtitle)
  return true
}
