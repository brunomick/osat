import { beforeEach, describe, expect, it, vi } from 'vitest'
import { get } from 'svelte/store'
import type { LockState } from './api'

// Todo lo que toca el backend o la interfaz se simula: acá se prueba solo cuándo se restaura la sesión.
const m = vi.hoisted(() => ({
  lockStatus: vi.fn(),
  lockNow: vi.fn(),
  lockUnlockHello: vi.fn(),
  lockWindowReady: vi.fn(),
  workspaceLastGet: vi.fn(),
  workspaceLastSet: vi.fn(),
  restore: vi.fn(),
  openTab: vi.fn(),
}))

vi.mock('./api', () => ({
  api: {
    lockStatus: m.lockStatus,
    lockNow: m.lockNow,
    lockUnlockHello: m.lockUnlockHello,
    lockWindowReady: m.lockWindowReady,
    workspaceLastGet: m.workspaceLastGet,
    workspaceLastSet: m.workspaceLastSet,
  },
  errorText: String,
}))
vi.mock('./dialog', () => ({ confirmDialog: vi.fn(), promptDialog: vi.fn() }))
vi.mock('./layout', async () => {
  const { writable } = await import('svelte/store')
  return { layout: writable(null), installLayout: vi.fn() }
})
vi.mock('./workspace', () => ({ restore: m.restore, snapshot: () => ({ tabs: [] }) }))
vi.mock('./stores', async () => {
  const { writable } = await import('svelte/store')
  return {
    tabs: writable([]),
    connections: writable([]),
    settings: writable({ general: { restoreSession: true } }),
    openTab: m.openTab,
    closeTab: vi.fn(),
    toast: vi.fn(),
  }
})

import { helloWhenVisible, lockNow, locked, lockState, refreshLock, unlockWithHello, untilUnlocked } from './lock'
import { startSession } from './workspaces'

const state = (p: Partial<LockState> = {}): LockState => ({ enabled: true, locked: true, helloAvailable: false, helloEnabled: false, ...p })
const flush = () => new Promise((r) => setTimeout(r, 0))

beforeEach(() => {
  vi.clearAllMocks()
  locked.set(true)
  lockState.set(null)
  m.workspaceLastGet.mockResolvedValue({ tabs: [] })
  m.workspaceLastSet.mockResolvedValue(undefined)
  m.restore.mockReturnValue({ tabs: [], skipped: [] })
})

describe('refreshLock / lockNow', () => {
  it('arranca bloqueado hasta que Go dice lo contrario', async () => {
    expect(get(locked)).toBe(true)
    m.lockStatus.mockResolvedValue(state({ enabled: false, locked: false }))
    await refreshLock()
    expect(get(locked)).toBe(false)
    expect(get(lockState)?.enabled).toBe(false)
  })

  it('lockNow cubre al instante y se queda con lo que confirma Go', async () => {
    locked.set(false)
    let confirm!: () => void
    m.lockNow.mockReturnValue(new Promise<void>((r) => (confirm = r)))
    m.lockStatus.mockResolvedValue(state())
    const p = lockNow()
    expect(get(locked)).toBe(true)
    expect(get(lockState)).toBeNull()
    confirm()
    await p
    expect(get(locked)).toBe(true)
    expect(get(lockState)?.enabled).toBe(true)
  })

  it('si Go no bloquea (sin contraseña), se vuelve a desbloquear', async () => {
    m.lockNow.mockResolvedValue(undefined)
    m.lockStatus.mockResolvedValue(state({ enabled: false, locked: false }))
    await lockNow()
    expect(get(locked)).toBe(false)
  })

  it('Hello cancelado: sigue bloqueado; verificado: se desbloquea', async () => {
    m.lockUnlockHello.mockResolvedValue(undefined)
    m.lockStatus.mockResolvedValueOnce(state())
    await unlockWithHello()
    expect(get(locked)).toBe(true)
    m.lockStatus.mockResolvedValueOnce(state({ locked: false }))
    await unlockWithHello()
    expect(get(locked)).toBe(false)
  })
})

describe('untilUnlocked', () => {
  it('se resuelve ya mismo si no está bloqueado', async () => {
    locked.set(false)
    await untilUnlocked()
  })

  it('espera al desbloqueo', async () => {
    const done = vi.fn()
    untilUnlocked().then(done)
    await flush()
    expect(done).not.toHaveBeenCalled()
    locked.set(false)
    await flush()
    expect(done).toHaveBeenCalledTimes(1)
  })
})

describe('startSession con osat bloqueado', () => {
  it('no restaura la última sesión ni empieza a guardar hasta el desbloqueo', async () => {
    const p = startSession(true)
    await flush()
    expect(m.workspaceLastGet).not.toHaveBeenCalled()
    expect(m.openTab).not.toHaveBeenCalled()

    locked.set(false)
    await p
    expect(m.workspaceLastGet).toHaveBeenCalledTimes(1)
    expect(m.restore).toHaveBeenCalledTimes(1)
  })

  it('sin bloqueo restaura enseguida', async () => {
    locked.set(false)
    await startSession(true)
    expect(m.workspaceLastGet).toHaveBeenCalledTimes(1)
  })

  it('sin las conexiones leídas no hace nada, ni espera', async () => {
    await startSession(false)
    expect(m.workspaceLastGet).not.toHaveBeenCalled()
  })
})

describe('helloWhenVisible', () => {
  it('con osat al frente lanza Hello enseguida', async () => {
    m.lockWindowReady.mockResolvedValue(true)
    const launch = vi.fn()
    helloWhenVisible(launch)
    await flush()
    expect(launch).toHaveBeenCalledTimes(1)
  })

  it('con osat minimizado o detrás de otra ventana espera, y lanza una sola vez al volver', async () => {
    vi.useFakeTimers()
    try {
      m.lockWindowReady.mockResolvedValue(false)
      const launch = vi.fn()
      helloWhenVisible(launch)
      await vi.advanceTimersByTimeAsync(10 * 60_000) // diez minutos minimizado: nada
      expect(launch).not.toHaveBeenCalled()
      m.lockWindowReady.mockResolvedValue(true)
      await vi.advanceTimersByTimeAsync(5_000)
      expect(launch).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(60_000)
      expect(launch).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('cancelado (se desbloqueó con la contraseña) no lanza nada', async () => {
    vi.useFakeTimers()
    try {
      m.lockWindowReady.mockResolvedValue(false)
      const launch = vi.fn()
      const cancel = helloWhenVisible(launch)
      await vi.advanceTimersByTimeAsync(3_000)
      cancel()
      m.lockWindowReady.mockResolvedValue(true)
      await vi.advanceTimersByTimeAsync(60_000)
      expect(launch).not.toHaveBeenCalled()
    } finally {
      vi.useRealTimers()
    }
  })
})
