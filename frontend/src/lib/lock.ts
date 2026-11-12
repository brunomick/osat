import { writable } from 'svelte/store'
import { api, type LockState } from './api'

// Bloqueo con contraseña en la interfaz. La verdad vive en Go (LockStatus): este store solo la refleja,
// así recargar el frontend no saltea el bloqueo. Arranca en true: hasta saber lo que dice Go, se está bloqueado.
export const locked = writable(true)

/** Lo último que dijo LockStatus, o null mientras se consulta (al arrancar y al bloquear). */
export const lockState = writable<LockState | null>(null)

/** Consulta a Go y actualiza los stores. Se llama al arrancar, al bloquear y al volver de Ajustes (Hello se cachea, pero igual no va en un intervalo). */
export async function refreshLock(): Promise<LockState> {
  const s = await api.lockStatus()
  lockState.set(s)
  locked.set(s.locked)
  return s
}

/** Bloquea osat. La pantalla de bloqueo aparece al instante; después Go confirma. Sin contraseña definida, Go no bloquea y se vuelve a desbloquear. */
export async function lockNow() {
  locked.set(true)
  lockState.set(null)
  await api.lockNow()
  await refreshLock()
}

/** Se resuelve cuando osat no está bloqueado (ya mismo, si no lo está). */
export function untilUnlocked(): Promise<void> {
  return new Promise((resolve) => {
    let off: (() => void) | undefined
    let done = false
    off = locked.subscribe((l) => {
      if (l) return
      done = true
      resolve()
      off?.() // el primer aviso llega antes de que `off` tenga valor
    })
    if (done) off()
  })
}

/** Lanza Windows Hello y, pase lo que pase (verificado, cancelado, sin Hello), consulta el estado. */
export async function unlockWithHello() {
  await api.lockUnlockHello()
  await refreshLock()
}

/** Cada cuánto se mira si osat volvió al frente mientras espera para lanzar Hello. */
const HELLO_READY_EVERY = 1000

/**
 * Llama a `launch` una sola vez, cuando la ventana de osat está al frente y no minimizada: con osat
 * minimizado o detrás de otra aplicación el diálogo de Hello aparecería minimizado o fuera de la vista.
 * (No alcanza con document.hasFocus: con la ventana minimizada, WebView2 puede seguir diciendo que tiene el foco.)
 * Devuelve la función que deja de esperar.
 */
export function helloWhenVisible(launch: () => void): () => void {
  let stopped = false
  let timer: ReturnType<typeof setTimeout> | undefined
  const check = async () => {
    const ready = await api.lockWindowReady().catch(() => false)
    if (stopped) return
    if (ready) {
      stopped = true
      launch()
    } else timer = setTimeout(check, HELLO_READY_EVERY)
  }
  check()
  return () => {
    stopped = true
    clearTimeout(timer)
  }
}
