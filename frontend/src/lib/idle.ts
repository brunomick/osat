// Bloqueo por inactividad: cuenta como actividad el teclado y el mouse dentro de osat (también lo que se
// escribe en una terminal o en un escritorio remoto: los eventos pasan por window en la fase de captura).

const ACTIVITY = ['keydown', 'pointerdown', 'pointermove', 'wheel'] as const

/**
 * Llama a `onIdle` una vez cuando pasan `minutes` minutos sin actividad. Con 0 (o menos) no hace nada.
 * Devuelve la función que lo detiene. Después de avisar no vuelve a contar: quien llama lo arranca de nuevo.
 */
export function startIdleTimer(minutes: number, onIdle: () => void): () => void {
  if (!(minutes > 0)) return () => {}
  const limit = minutes * 60_000
  let last = Date.now()
  let timer: ReturnType<typeof setTimeout>

  // Un solo temporizador: al vencer mira cuánto pasó desde la última actividad y, si falta, espera lo que falte
  // (así pointermove, que llega a montones, solo anota la hora; y si la PC estuvo dormida, bloquea al despertar).
  const check = () => {
    const left = last + limit - Date.now()
    if (left > 0) {
      timer = setTimeout(check, left)
      return
    }
    stop()
    onIdle()
  }
  const touch = () => {
    last = Date.now()
  }
  const stop = () => {
    clearTimeout(timer)
    for (const e of ACTIVITY) window.removeEventListener(e, touch, true)
  }

  for (const e of ACTIVITY) window.addEventListener(e, touch, { capture: true, passive: true })
  timer = setTimeout(check, limit)
  return stop
}
