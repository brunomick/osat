import { derived, writable, type Readable } from 'svelte/store'

// Capas de osat que cubren la ventana (diálogos, menús, arrastres…). Las vistas nativas (sitios web) se
// dibujan siempre encima del HTML, así que se ocultan mientras haya alguna capa abierta. Cada capa llama a
// holdOverlay() al abrirse y a lo que devuelve al cerrarse.

const holds = writable(0)

/** Hay al menos una capa de osat abierta. */
export const overlayOpen: Readable<boolean> = derived(holds, (n) => n > 0)

/** Marca una capa abierta. Devuelve la función que la libera (liberar dos veces la misma no cuenta de más). */
export function holdOverlay(): () => void {
  let held = true
  holds.update((n) => n + 1)
  return () => {
    if (!held) return
    held = false
    holds.update((n) => n - 1)
  }
}

/** Para una capa que se muestra según una condición: devuelve una función que toma y suelta la marca según `on`. */
export function overlayHolder(): (on: boolean) => void {
  let release: (() => void) | null = null
  return (on) => {
    if (on && !release) release = holdOverlay()
    else if (!on && release) (release(), (release = null))
  }
}
