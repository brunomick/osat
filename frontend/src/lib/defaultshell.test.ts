import { describe, expect, it, vi } from 'vitest'
import type { LocalShell } from './api'
import { migrateDefaultShell, SHELL_KEY, shellToMigrate } from './defaultshell'

const sh = (id: string): LocalShell => ({ id, name: id.toUpperCase() })
const shells = [sh('bash'), sh('zsh'), sh('fish')]

// Un localStorage mínimo en memoria.
const storage = (init: Record<string, string> = {}) => {
  const data = { ...init }
  return {
    data,
    getItem: (k: string) => data[k] ?? null,
    removeItem: (k: string) => void delete data[k],
  }
}

describe('shellToMigrate', () => {
  it('conserva la que se usaba si sigue instalada', () => {
    expect(shellToMigrate('zsh', shells)).toBe('zsh')
  })
  it('si ya no está instalada toma la primera detectada', () => {
    expect(shellToMigrate('powershell', shells)).toBe('bash')
  })
  it('sin valor guardado toma la primera detectada', () => {
    expect(shellToMigrate(null, shells)).toBe('bash')
  })
  it('sin terminales detectadas no hay nada que migrar', () => {
    expect(shellToMigrate('zsh', [])).toBe('')
  })
})

describe('migrateDefaultShell', () => {
  it('guarda la vieja en los ajustes y borra la clave', async () => {
    const st = storage({ [SHELL_KEY]: 'fish' })
    const save = vi.fn().mockResolvedValue(undefined)
    await migrateDefaultShell({ current: '', shells, storage: st, save })
    expect(save).toHaveBeenCalledWith('fish')
    expect(st.data[SHELL_KEY]).toBeUndefined()
  })

  it('no toca nada si los ajustes ya tienen una terminal (solo limpia la clave vieja)', async () => {
    const st = storage({ [SHELL_KEY]: 'fish' })
    const save = vi.fn()
    await migrateDefaultShell({ current: 'zsh', shells, storage: st, save })
    expect(save).not.toHaveBeenCalled()
    expect(st.data[SHELL_KEY]).toBeUndefined()
  })

  it('sin terminales detectadas espera: no guarda ni borra la clave', async () => {
    const st = storage({ [SHELL_KEY]: 'fish' })
    const save = vi.fn()
    await migrateDefaultShell({ current: '', shells: [], storage: st, save })
    expect(save).not.toHaveBeenCalled()
    expect(st.data[SHELL_KEY]).toBe('fish')
  })

  it('si no se pudo guardar, falla y conserva la clave para reintentar', async () => {
    const st = storage({ [SHELL_KEY]: 'fish' })
    const save = vi.fn().mockRejectedValue(new Error('disco lleno'))
    await expect(migrateDefaultShell({ current: '', shells, storage: st, save })).rejects.toThrow('disco lleno')
    expect(st.data[SHELL_KEY]).toBe('fish')
  })

  it('sin clave vieja toma la primera detectada', async () => {
    const save = vi.fn().mockResolvedValue(undefined)
    await migrateDefaultShell({ current: '', shells, storage: storage(), save })
    expect(save).toHaveBeenCalledWith('bash')
  })
})
