// Arrastre de archivos entre exploradores (SFTP y "Este equipo"), dentro de
// la ventana. Es un arrastre propio (no HTML5, que choca con el drop de
// archivos de Wails): cada explorador se registra en fsViews con el id de su
// pestaña y marca su lista con data-fs-tab; las carpetas llevan data-drop.
// Qué pasa al soltar (mover, subir, descargar) lo decide el explorador destino.
import { writable } from 'svelte/store'
import { platform } from './platform'
import { tr } from './i18n'

export type FsSide = 'remote' | 'local'

/** Desde dónde se arrastra. */
export interface FsSource {
  tabId: string
  side: FsSide
  connId?: string
  sessionId?: string
}

export interface FsView {
  cwd(): string
  /** Texto de lo que haría soltar en dir ("Mover … a …"), o null si no se puede. */
  verb(src: FsSource, paths: string[], dir: string): string | null
  drop(src: FsSource, paths: string[], dir: string): void
  refresh(): void
}

export const fsViews = new Map<string, FsView>()

export interface FileDrag {
  source: FsSource
  paths: string[]
  x: number
  y: number
  target: { tabId: string; dir: string } | null
  label: string
}

export const fileDrag = writable<FileDrag | null>(null)

export const baseName = (p: string) => p.split(/[\\/]/).filter(Boolean).pop() || p
export const describe = (paths: string[]) => (paths.length === 1 ? `“${baseName(paths[0])}”` : tr('drag.items', { count: paths.length }))

/** Carpeta que contiene p ("/a/b" → "/a", "C:\a" → "C:\"). */
export function parentOf(p: string) {
  const d = p.replace(/[\\/]+$/, '').replace(/[\\/][^\\/]*$/, '')
  if (/^[A-Za-z]:$/.test(d)) return d + '\\'
  return d || '/'
}

/** Ya están todos en dir: soltarlos ahí no cambia nada. */
export const allIn = (paths: string[], dir: string) => paths.every((p) => parentOf(p) === dir)

/**
 * Arranca el arrastre (el botón ya está presionado y el mouse se movió).
 * Si el cursor sale de la ventana, termina y llama a out (arrastre nativo al
 * sistema); end se llama siempre al terminar.
 */
export function beginFileDrag(source: FsSource, paths: string[], out: (paths: string[]) => void, end?: () => void) {
  let last: FileDrag | null = null

  const finish = () => {
    window.removeEventListener('mousemove', move)
    window.removeEventListener('mouseup', up)
    window.removeEventListener('keydown', key, true)
    fileDrag.set(null)
    end?.()
  }

  const move = (ev: MouseEvent) => {
    if (ev.clientX < 0 || ev.clientY < 0 || ev.clientX >= window.innerWidth || ev.clientY >= window.innerHeight) {
      // Solo en Windows hay arrastre nativo al sistema; en el resto soltar afuera no hace nada.
      if (platform === 'windows') {
        finish()
        out(paths)
        return
      }
    }
    const el = document.elementFromPoint(ev.clientX, ev.clientY) as HTMLElement | null
    const viewEl = el?.closest<HTMLElement>('[data-fs-tab]')
    const tabId = viewEl?.dataset.fsTab
    const view = tabId ? fsViews.get(tabId) : undefined
    let target: FileDrag['target'] = null
    let label =
      platform === 'windows'
        ? tr('drag.dropFolderOrOut', { what: describe(paths) })
        : tr('drag.dropFolder', { what: describe(paths) })
    if (view && tabId) {
      const dir = el?.closest<HTMLElement>('[data-drop]')?.dataset.drop ?? view.cwd()
      const verb = paths.includes(dir) ? null : view.verb(source, paths, dir)
      if (verb) {
        target = { tabId, dir }
        label = verb
      }
    }
    last = { source, paths, x: ev.clientX, y: ev.clientY, target, label }
    fileDrag.set(last)
  }

  const up = () => {
    const t = last?.target
    finish()
    if (t) fsViews.get(t.tabId)?.drop(source, paths, t.dir)
  }

  const key = (e: KeyboardEvent) => {
    if (e.key !== 'Escape') return
    e.preventDefault()
    e.stopPropagation()
    last = null
    finish()
  }

  window.addEventListener('mousemove', move)
  window.addEventListener('mouseup', up)
  window.addEventListener('keydown', key, true)
}
