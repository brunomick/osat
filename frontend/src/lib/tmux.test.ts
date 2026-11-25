import { describe, expect, it } from 'vitest'
import type { TmuxSession } from './api'
import { nextShellName, othersInside, ownerKind, sortShells } from './tmux'

const s = (over: Partial<TmuxSession>): TmuxSession => ({ id: '$0', name: 'x', attached: 0, activity: 0, windows: 1, panes: 1, owner: '', ...over })

describe('shells persistentes', () => {
  it('distingue las mías, las de otro osat y las armadas a mano', () => {
    expect(ownerKind(s({ owner: 'bruno@PC' }), 'bruno@PC')).toBe('mine')
    expect(ownerKind(s({ owner: 'juan@PC-JUAN' }), 'bruno@PC')).toBe('other')
    expect(ownerKind(s({ owner: '' }), 'bruno@PC')).toBe('manual')
  })

  it('cuenta solo a los de afuera: los clientes menos mis pestañas conectadas a esa sesión', () => {
    const tabs = [
      { connId: 'c1', status: 'ready', tmux: { id: '$3', name: 'a' } },
      { connId: 'c1', status: 'ready', tmux: { id: '$3', name: 'a' } },
      { connId: 'c1', status: 'closed', tmux: { id: '$3', name: 'a' } },
      { connId: 'c2', status: 'ready', tmux: { id: '$3', name: 'a' } },
      { connId: 'c1', status: 'ready' },
    ] as const
    expect(othersInside(s({ id: '$3', attached: 3 }), 'c1', tabs)).toBe(1)
    expect(othersInside(s({ id: '$3', attached: 1 }), 'c1', tabs)).toBe(0) // la lista pudo quedar vieja
  })

  it('ordena por actividad, la más reciente primero', () => {
    const out = sortShells([s({ id: '$1', activity: 10 }), s({ id: '$2', activity: 30 }), s({ id: '$3', activity: 20 })])
    expect(out.map((x) => x.id)).toEqual(['$2', '$3', '$1'])
  })

  it('propone un nombre libre a partir del servidor, sin . ni : (tmux los cambia)', () => {
    expect(nextShellName('web.prod', [])).toBe('web_prod 1')
    expect(nextShellName('db', [s({ name: 'db 1' }), s({ name: 'db 2' })])).toBe('db 3')
  })
})
