import { describe, expect, it } from 'vitest'
import { backFrom, parseTarget } from './mobile'

describe('backFrom', () => {
  it('de una sesión abierta, a Sesiones', () => {
    expect(backFrom({ kind: 'terminal' }, [{ kind: 'terminal' }])).toBe('sessions')
  })
  it('de una sesión que se cerró y era la última, a Inicio', () => {
    expect(backFrom({ kind: 'terminal' }, [{ kind: 'settings' }])).toBe('home')
  })
  it('de ajustes o un formulario, siempre a Inicio', () => {
    expect(backFrom({ kind: 'settings' }, [{ kind: 'settings' }, { kind: 'sftp' }])).toBe('home')
    expect(backFrom({ kind: 'connection' }, [{ kind: 'terminal' }])).toBe('home')
  })
})

describe('parseTarget', () => {
  it('separa usuario, host y puerto', () => {
    expect(parseTarget('root@10.0.0.1:2222')).toEqual({ user: 'root', host: '10.0.0.1', port: 2222 })
  })
  it('usuario y puerto son opcionales', () => {
    expect(parseTarget('  servidor.local ')).toEqual({ user: '', host: 'servidor.local', port: 0 })
  })
  it('rechaza lo que no es un destino', () => {
    expect(parseTarget('')).toBeNull()
    expect(parseTarget('dos palabras')).toBeNull()
    expect(parseTarget('a@b@c')).toBeNull()
  })
})

