import { clipboardGet, clipboardSet } from './api'

/** Cuánto queda una contraseña copiada en el portapapeles. */
export const SECRET_CLIPBOARD_MS = 30_000

/**
 * Copia un secreto y, pasado `ms`, limpia el portapapeles si todavía lo tiene (si la persona copió otra
 * cosa después, no la toca). El timer no depende de ningún componente: sigue aunque se cierre la pestaña.
 */
export async function copySecret(text: string, ms = SECRET_CLIPBOARD_MS, io = { get: clipboardGet, set: clipboardSet }): Promise<void> {
  await io.set(text)
  setTimeout(async () => {
    try {
      if ((await io.get()) === text) await io.set('')
    } catch {
      // sin acceso al portapapeles: no hay nada que limpiar
    }
  }, ms)
}
