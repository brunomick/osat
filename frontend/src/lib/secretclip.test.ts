import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { copySecret } from './secretclip'

describe('copySecret', () => {
  let clip = ''
  const io = { get: async () => clip, set: async (t: string) => void (clip = t) }
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('limpia el portapapeles a los 30 s si sigue la contraseña', async () => {
    await copySecret('hunter2', undefined, io)
    expect(clip).toBe('hunter2')
    await vi.advanceTimersByTimeAsync(29_999)
    expect(clip).toBe('hunter2')
    await vi.advanceTimersByTimeAsync(1)
    expect(clip).toBe('')
  })

  it('no toca lo que se copió después', async () => {
    await copySecret('hunter2', undefined, io)
    clip = 'otra cosa'
    await vi.advanceTimersByTimeAsync(30_000)
    expect(clip).toBe('otra cosa')
  })
})
