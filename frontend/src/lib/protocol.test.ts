import { describe, expect, it } from 'vitest'
import { canBeJump, defaultPort, formFields, isMongo, protocolOf } from './protocol'

describe('protocol', () => {
  it('puerto por defecto de cada protocolo', () => {
    expect(defaultPort).toEqual({ ssh: 22, rdp: 3389, vnc: 5900, mongo: 27017 })
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
    expect(canBeJump({ protocol: 'mongo' })).toBe(false)
  })
  it('VNC muestra solo host, puerto y contraseña', () => {
    expect(formFields('vnc')).toEqual({ uri: false, user: false, domain: false, gateway: false, auth: false, advanced: false, defaults: true })
  })
  it('RDP y SSH conservan sus campos', () => {
    expect(formFields('rdp')).toEqual({ uri: false, user: true, domain: true, gateway: true, auth: false, advanced: false, defaults: true })
    expect(formFields('ssh')).toEqual({ uri: false, user: true, domain: false, gateway: false, auth: true, advanced: true, defaults: true })
  })
  it('MongoDB usa la cadena de conexión y no las credenciales por defecto', () => {
    expect(formFields('mongo')).toEqual({ uri: true, user: false, domain: false, gateway: false, auth: false, advanced: false, defaults: false })
    expect(isMongo({ protocol: 'mongo' })).toBe(true)
    expect(isMongo({})).toBe(false)
    expect(isMongo(undefined)).toBe(false)
  })
})
