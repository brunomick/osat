import { beforeEach, describe, expect, it, vi } from 'vitest'

const m = vi.hoisted(() => ({ choiceDialog: vi.fn(), toast: vi.fn() }))

vi.mock('./dialog', () => ({ choiceDialog: m.choiceDialog }))
// stores.ts lee localStorage al cargarse: se reemplaza entero.
vi.mock('./stores', async () => {
  const { writable } = await import('svelte/store')
  return { toast: m.toast, settings: writable({ files: { onConflict: 'ask' } }) }
})

import { settings } from './stores'
import { transfer } from './transfer'

const conflict = () => new Error('OSAT:' + JSON.stringify({ code: 'CONFLICT', message: 'ya existe', data: { count: '1', names: 'a.txt' } }))
const setPolicy = (onConflict: string) => settings.set({ files: { onConflict } } as never)

beforeEach(() => {
  vi.clearAllMocks()
  setPolicy('ask')
})

describe('transfer', () => {
  it('arranca con la política de los ajustes', async () => {
    for (const policy of ['ask', 'overwrite', 'skip']) {
      setPolicy(policy)
      const run = vi.fn().mockResolvedValue(undefined)
      await transfer(run)
      expect(run).toHaveBeenCalledTimes(1)
      expect(run).toHaveBeenCalledWith(policy)
    }
    expect(m.choiceDialog).not.toHaveBeenCalled()
  })

  it('con "preguntar" y un conflicto, pregunta y repite con la respuesta', async () => {
    const run = vi.fn().mockRejectedValueOnce(conflict()).mockResolvedValue(undefined)
    m.choiceDialog.mockResolvedValue('overwrite')
    await transfer(run)
    expect(run.mock.calls.map((c) => c[0])).toEqual(['ask', 'overwrite'])
  })

  it('si se cancela la pregunta no repite', async () => {
    const run = vi.fn().mockRejectedValue(conflict())
    m.choiceDialog.mockResolvedValue(null)
    await transfer(run)
    expect(run).toHaveBeenCalledTimes(1)
  })

  it('otros errores se muestran', async () => {
    const run = vi.fn().mockRejectedValue(new Error('sin permiso'))
    await transfer(run)
    expect(m.toast).toHaveBeenCalledWith('sin permiso', 'error')
  })
})
