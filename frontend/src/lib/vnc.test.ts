import { describe, expect, it } from 'vitest'
import { classifyEnd, onlyPassword, type VncFacts } from './vnc'

const facts = (p: Partial<VncFacts> = {}): VncFacts => ({
  connected: false,
  clean: true,
  cancelled: false,
  unsupportedAuth: false,
  securityFailure: null,
  passwordSent: false,
  ...p,
})

describe('onlyPassword', () => {
  it('solo la contraseña VNC es soportada', () => {
    expect(onlyPassword(['password'])).toBe(true)
    expect(onlyPassword(['username', 'password'])).toBe(false) // Plain, RealVNC, Apple...
    expect(onlyPassword(['username', 'password', 'target'])).toBe(false)
    expect(onlyPassword([])).toBe(false)
  })
})

describe('classifyEnd', () => {
  it('la contraseña rechazada se vuelve a pedir, con el motivo del servidor', () => {
    expect(classifyEnd(facts({ securityFailure: { reason: 'Authentication failed' }, passwordSent: true, clean: false }))).toEqual({
      kind: 'badPassword',
      reason: 'Authentication failed',
    })
  })
  it('un rechazo sin que se haya mandado contraseña no es una contraseña mala', () => {
    expect(classifyEnd(facts({ securityFailure: { reason: 'Too many security failures' }, clean: false }))).toEqual({
      kind: 'refused',
      reason: 'Too many security failures',
    })
  })
  it('una autenticación no soportada tiene su mensaje', () => {
    expect(classifyEnd(facts({ unsupportedAuth: true, clean: false }))).toEqual({ kind: 'unsupportedAuth' })
  })
  it('cortar antes de conectar, sin más datos, se explica como saludo fallido (VeNCrypt/TLS)', () => {
    expect(classifyEnd(facts({ clean: false }))).toEqual({ kind: 'handshake' })
  })
  it('cancelar el pedido de contraseña no es un error', () => {
    expect(classifyEnd(facts({ cancelled: true }))).toEqual({ kind: 'cancelled' })
  })
  it('con la sesión abierta: cierre normal o corte', () => {
    expect(classifyEnd(facts({ connected: true, clean: true }))).toEqual({ kind: 'closed' })
    expect(classifyEnd(facts({ connected: true, clean: false }))).toEqual({ kind: 'lost' })
  })
})
