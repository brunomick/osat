import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { delayFor, NeedsUserError, reconnect, type ReconnectState } from './reconnect'

// Promesas que se resuelven a mano, para controlar cuándo termina cada intento.
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => ((resolve = res), (reject = rej)))
  return { promise, resolve, reject }
}

// Deja correr las promesas pendientes (los .then del ciclo).
const flush = () => vi.advanceTimersByTimeAsync(0)

let online: EventTarget
let states: ReconnectState<string>[]
const onState = (s: ReconnectState<string>) => states.push(s)
const last = () => states[states.length - 1]

beforeEach(() => {
  vi.useFakeTimers()
  online = new EventTarget()
  states = []
})
afterEach(() => {
  vi.useRealTimers()
})

describe('delayFor', () => {
  it('1, 2, 4, 8, 16 y después siempre 30 segundos', () => {
    expect([1, 2, 3, 4, 5, 6, 7, 20].map(delayFor)).toEqual([1, 2, 4, 8, 16, 30, 30, 30])
  })
})

describe('reconnect', () => {
  it('sigue la secuencia de esperas entre intentos fallidos', async () => {
    const attempt = vi.fn().mockRejectedValue(new Error('connection refused'))
    reconnect(attempt, onState, { online })
    expect(last()).toEqual({ phase: 'waiting', attempt: 1, seconds: 1 })
    let calls = 0
    for (const wait of [1, 2, 4, 8, 16, 30, 30]) {
      await vi.advanceTimersByTimeAsync(wait * 1000 - 1)
      expect(attempt).toHaveBeenCalledTimes(calls) // todavía no
      await vi.advanceTimersByTimeAsync(1)
      expect(attempt).toHaveBeenCalledTimes(++calls)
    }
    expect(last()).toEqual({ phase: 'waiting', attempt: 8, seconds: 30 })
  })

  it('avisa cada segundo lo que falta para el próximo intento', async () => {
    reconnect(vi.fn().mockRejectedValue(new Error('x')), onState, { online })
    await vi.advanceTimersByTimeAsync(1000) // intento 1, falla
    await vi.advanceTimersByTimeAsync(2000) // intento 2, falla
    await vi.advanceTimersByTimeAsync(2000)
    expect(states.filter((s) => s.phase === 'waiting' && s.attempt === 3)).toEqual([
      { phase: 'waiting', attempt: 3, seconds: 4 },
      { phase: 'waiting', attempt: 3, seconds: 3 },
      { phase: 'waiting', attempt: 3, seconds: 2 },
    ])
    expect(states).toContainEqual({ phase: 'trying', attempt: 2 })
  })

  it('conecta al tercer intento y deja de reintentar', async () => {
    const attempt = vi.fn().mockRejectedValueOnce(new Error('a')).mockRejectedValueOnce(new Error('b')).mockResolvedValueOnce('sesion')
    reconnect(attempt, onState, { online })
    await vi.advanceTimersByTimeAsync(1000 + 2000 + 4000)
    expect(attempt).toHaveBeenCalledTimes(3)
    expect(last()).toEqual({ phase: 'connected', result: 'sesion' })
    await vi.advanceTimersByTimeAsync(120_000)
    expect(attempt).toHaveBeenCalledTimes(3)
    expect(states.filter((s) => s.phase === 'connected')).toHaveLength(1)
  })

  it('NeedsUserError corta el ciclo', async () => {
    const err = new NeedsUserError('Ingresá la contraseña.')
    const attempt = vi.fn().mockRejectedValueOnce(new Error('red')).mockRejectedValueOnce(err)
    reconnect(attempt, onState, { online })
    await vi.advanceTimersByTimeAsync(1000 + 2000)
    expect(last()).toEqual({ phase: 'needsUser', error: err })
    await vi.advanceTimersByTimeAsync(120_000)
    online.dispatchEvent(new Event('online'))
    await flush()
    expect(attempt).toHaveBeenCalledTimes(2)
  })

  it('cancelar mientras espera no intenta más', async () => {
    const attempt = vi.fn().mockRejectedValue(new Error('x'))
    const r = reconnect(attempt, onState, { online })
    await vi.advanceTimersByTimeAsync(1000)
    r.cancel()
    expect(last()).toEqual({ phase: 'cancelled' })
    await vi.advanceTimersByTimeAsync(120_000)
    online.dispatchEvent(new Event('online'))
    r.retryNow()
    await flush()
    expect(attempt).toHaveBeenCalledTimes(1)
    r.cancel()
    expect(states.filter((s) => s.phase === 'cancelled')).toHaveLength(1)
  })

  it('si un intento en curso conecta después de cancelar, se descarta', async () => {
    const d = deferred<string>()
    const discard = vi.fn()
    const r = reconnect(() => d.promise, onState, { online, discard })
    await vi.advanceTimersByTimeAsync(1000)
    expect(last()).toEqual({ phase: 'trying', attempt: 1 })
    r.cancel()
    d.resolve('tarde')
    await flush()
    expect(discard).toHaveBeenCalledWith('tarde')
    expect(last()).toEqual({ phase: 'cancelled' })
  })

  it('reintentar ya no espera lo que falta', async () => {
    const attempt = vi.fn().mockRejectedValueOnce(new Error('x')).mockResolvedValueOnce('ok')
    const r = reconnect(attempt, onState, { online })
    r.retryNow()
    await flush()
    expect(attempt).toHaveBeenCalledTimes(1)
    expect(last()).toEqual({ phase: 'waiting', attempt: 2, seconds: 2 })
    r.retryNow()
    await flush()
    expect(last()).toEqual({ phase: 'connected', result: 'ok' })
  })

  it('reintentar ya durante un intento no arranca otro', async () => {
    const d = deferred<string>()
    const attempt = vi.fn(() => d.promise)
    const r = reconnect(attempt, onState, { online })
    r.retryNow()
    r.retryNow()
    online.dispatchEvent(new Event('online'))
    expect(attempt).toHaveBeenCalledTimes(1)
    d.resolve('ok')
    await flush()
    expect(last()).toEqual({ phase: 'connected', result: 'ok' })
  })

  it('cuando vuelve la red intenta enseguida', async () => {
    const attempt = vi.fn().mockRejectedValueOnce(new Error('x')).mockRejectedValueOnce(new Error('x')).mockResolvedValueOnce('ok')
    reconnect(attempt, onState, { online })
    await vi.advanceTimersByTimeAsync(1000 + 2000) // dos intentos fallidos; ahora espera 4 s
    expect(last()).toEqual({ phase: 'waiting', attempt: 3, seconds: 4 })
    online.dispatchEvent(new Event('online'))
    await flush()
    expect(attempt).toHaveBeenCalledTimes(3)
    expect(last()).toEqual({ phase: 'connected', result: 'ok' })
  })

  it('después de conectar deja de escuchar el aviso de la red', async () => {
    const attempt = vi.fn().mockResolvedValue('ok')
    reconnect(attempt, onState, { online })
    await vi.advanceTimersByTimeAsync(1000)
    online.dispatchEvent(new Event('online'))
    await flush()
    expect(attempt).toHaveBeenCalledTimes(1)
  })
})
