import { describe, expect, it } from 'vitest'
import { canBeJump, defaultPort, formFields, protocolOf } from './protocol'

describe('protocol', () => {
  it('puerto por defecto de cada protocolo', () => {
    expect(defaultPort).toEqual({ ssh: 22, rdp: 3389, vnc: 5900 })
  })
  it('una conexión sin protocolo es SSH', () => {
    expect(protocolOf({})).toBe('ssh')
    expect(protocolOf({ protocol: 'vnc' })).toBe('vnc')
  })
  it('solo las conexiones SSH sirven de salto', () => {
    expect(canBeJump({})).toBe(true)
    expect(canBeJump({ protocol: 'ssh' })).toBe(true)
    expect(canBeJump({ protocol: 'rdp' })).toBe(false)
    expect(canBeJump({ protocol: 'vnc' })).toBe(false)
  })
  it('VNC muestra solo host, puerto y contraseña', () => {
    expect(formFields('vnc')).toEqual({ user: false, domain: false, gateway: false, auth: false, advanced: false })
  })
  it('RDP y SSH conservan sus campos', () => {
    expect(formFields('rdp')).toEqual({ user: true, domain: true, gateway: true, auth: false, advanced: false })
    expect(formFields('ssh')).toEqual({ user: true, domain: false, gateway: false, auth: true, advanced: true })
  })
})
