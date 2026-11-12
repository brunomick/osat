// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { startIdleTimer } from './idle'

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

const seg = (n: number) => vi.advanceTimersByTime(n * 1000)

describe('startIdleTimer', () => {
  it('no avisa a los 59 s y sí a los 60 s, una sola vez', () => {
    const cb = vi.fn()
    const stop = startIdleTimer(1, cb)
    seg(59)
    expect(cb).not.toHaveBeenCalled()
    seg(1)
    expect(cb).toHaveBeenCalledTimes(1)
    seg(300)
    expect(cb).toHaveBeenCalledTimes(1)
    stop()
  })

  it('un keydown a los 30 s lo corre a los 90 s', () => {
    const cb = vi.fn()
    const stop = startIdleTimer(1, cb)
    seg(30)
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'a' }))
    seg(59)
    expect(cb).not.toHaveBeenCalled() // 89 s
    seg(1)
    expect(cb).toHaveBeenCalledTimes(1) // 90 s
    stop()
  })

  it('el mouse y la rueda también cuentan como actividad', () => {
    for (const ev of ['pointerdown', 'pointermove', 'wheel']) {
      const cb = vi.fn()
      const stop = startIdleTimer(1, cb)
      seg(30)
      window.dispatchEvent(new Event(ev))
      seg(59)
      expect(cb, ev).not.toHaveBeenCalled()
      stop()
    }
  })

  it('la actividad dentro de un elemento (fase de captura) también cuenta', () => {
    const cb = vi.fn()
    const el = document.createElement('div')
    document.body.append(el)
    el.addEventListener('keydown', (e) => e.stopPropagation()) // como una terminal que se queda con la tecla
    const stop = startIdleTimer(1, cb)
    seg(30)
    el.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true }))
    seg(59)
    expect(cb).not.toHaveBeenCalled()
    stop()
    el.remove()
  })

  it('la función devuelta lo detiene', () => {
    const cb = vi.fn()
    const stop = startIdleTimer(1, cb)
    seg(30)
    stop()
    seg(600)
    expect(cb).not.toHaveBeenCalled()
  })

  it('con 0 minutos nunca avisa', () => {
    const cb = vi.fn()
    const stop = startIdleTimer(0, cb)
    seg(24 * 3600)
    expect(cb).not.toHaveBeenCalled()
    stop()
  })
})
