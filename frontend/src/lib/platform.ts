// Sistema operativo en el que corre osat, para lo que cambia entre Windows,
// macOS y Linux (atajos, textos, barra de título). main.ts lo carga antes de
// montar la app, así las vistas lo leen sin esperar.

export type Platform = 'windows' | 'darwin' | 'linux'

export let platform: Platform = 'windows'

export async function loadPlatform() {
  try {
    platform = (await window.runtime.Environment()).platform
  } catch {
    // sin el runtime de Wails (el frontend suelto en un navegador): queda Windows
  }
}

export const isMac = () => platform === 'darwin'

/** Un atajo como se escribe en este sistema: en macOS "Ctrl+Shift+B" es "⇧⌘B" (Cmd hace de Ctrl). */
export function keys(combo: string) {
  if (!isMac()) return combo
  const parts = combo.split('+')
  const key = parts.pop()
  const marks: Record<string, string> = { Alt: '⌥', Shift: '⇧', Ctrl: '⌘' } // en el orden de macOS
  return Object.keys(marks).filter((m) => parts.includes(m)).map((m) => marks[m]).join('') + key
}

/** "Mostrar en el Explorador" / "Abrir en el Finder"…: la frase entera según el sistema. `tf` es `tr` o el store `$t`. */
export function fileManagerText(tf: (key: string) => string, action: 'showIn' | 'openIn') {
  return tf(`files.${action}.${platform}`)
}

/** En la app de Android (osat-android.js marca el <html> antes de que arranque el frontend). */
export const isAndroid = () => document.documentElement.classList.contains('android')
