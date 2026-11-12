// Reconexión automática de las terminales SSH y los exploradores SFTP cuando
// se cae la conexión: reintenta con esperas crecientes hasta que conecta, el
// usuario cancela o hace falta algo que solo el usuario puede resolver.

/** Esperas antes de cada intento, en segundos; después del último se repite. */
export const DELAYS = [1, 2, 4, 8, 16, 30]

/** Espera antes del intento n (desde 1). */
export const delayFor = (attempt: number) => DELAYS[Math.min(attempt, DELAYS.length) - 1]

/**
 * El intento no puede seguir sin preguntarle al usuario (contraseña, huella,
 * autenticación fallida, conexión borrada): corta el ciclo y la vista vuelve
 * al "Presioná Enter" de siempre.
 */
export class NeedsUserError extends Error {}

export type ReconnectState<T> =
  | { phase: 'waiting'; attempt: number; seconds: number } // falta `seconds` para el intento `attempt`
  | { phase: 'trying'; attempt: number }
  | { phase: 'connected'; result: T }
  | { phase: 'needsUser'; error: NeedsUserError }
  | { phase: 'cancelled' }

export interface Reconnector {
  /** Intenta ya, sin esperar lo que falta (Enter). */
  retryNow(): void
  /** Deja de reintentar (Esc). */
  cancel(): void
}

/**
 * Arranca el ciclo: espera, llama a attempt y, si falla por la red, vuelve a
 * esperar. onState avisa cada cambio (y cada segundo de la espera). Si el
 * sistema avisa que volvió la red (evento `online` de online), intenta
 * enseguida. Si se cancela mientras un intento está en curso y ese intento
 * conecta, el resultado se entrega a discard para que lo cierre.
 */
export function reconnect<T>(
  attempt: () => Promise<T>,
  onState: (s: ReconnectState<T>) => void,
  opts: { online?: EventTarget; discard?: (result: T) => void } = {},
): Reconnector {
  const online = opts.online ?? (typeof window !== 'undefined' ? window : undefined)
  let n = 1
  let left = 0
  let timer: ReturnType<typeof setInterval> | undefined
  let trying = false
  let done = false

  function finish(s: ReconnectState<T>) {
    done = true
    clearInterval(timer)
    online?.removeEventListener('online', retryNow)
    onState(s)
  }

  function wait() {
    left = delayFor(n)
    onState({ phase: 'waiting', attempt: n, seconds: left })
    timer = setInterval(() => {
      left--
      if (left <= 0) retryNow()
      else onState({ phase: 'waiting', attempt: n, seconds: left })
    }, 1000)
  }

  function retryNow() {
    if (done || trying) return
    clearInterval(timer)
    trying = true
    onState({ phase: 'trying', attempt: n })
    attempt().then(
      (result) => {
        trying = false
        if (done) opts.discard?.(result)
        else finish({ phase: 'connected', result })
      },
      (e) => {
        trying = false
        if (done) return
        if (e instanceof NeedsUserError) return finish({ phase: 'needsUser', error: e })
        n++
        wait()
      },
    )
  }

  online?.addEventListener('online', retryNow)
  wait()
  return {
    retryNow,
    cancel: () => {
      if (!done) finish({ phase: 'cancelled' })
    },
  }
}
