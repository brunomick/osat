import { writable } from 'svelte/store'
import { api } from './api'
import { settings } from './stores'
import { appThemes, type UiTheme } from './termthemes'

/** El tema de la interfaz ya resuelto (con "Igual que el sistema", el del sistema operativo). */
export const uiTheme = writable<UiTheme>(resolveAppearance(document.documentElement.dataset.theme ?? '', false))

const KEY = 'osat.theme' // lo lee index.html antes de dibujar, para que la pantalla de carga no parpadee
const query = () => matchMedia('(prefers-color-scheme: light)')

export function resolveAppearance(appearance: string, systemLight: boolean): UiTheme {
  if (appearance === 'system') return systemLight ? 'light' : 'dark'
  return appThemes.includes(appearance as UiTheme) ? (appearance as UiTheme) : 'dark'
}

let appearance = 'dark'
let applied: UiTheme | null = null

function apply() {
  const theme = resolveAppearance(appearance, query().matches)
  if (theme === applied) return
  applied = theme
  document.documentElement.dataset.theme = theme
  try {
    localStorage.setItem(KEY, theme)
  } catch {
    // sin almacenamiento local la pantalla de carga queda oscura
  }
  uiTheme.set(theme)
  api.setWindowTheme(theme).catch(() => {}) // fuera de la app no hay ventana que pintar
}

/** Aplica el ajuste General.Apariencia y lo sigue; con "sistema", también al modo del sistema operativo. */
export function initTheme() {
  let first = true
  settings.subscribe((s) => {
    // La primera vez es el valor de reserva, no el de settings.json: se espera a los ajustes reales.
    if (first) return void (first = false)
    appearance = s.general.appearance
    apply()
  })
  query().addEventListener('change', () => appearance === 'system' && apply())
}
