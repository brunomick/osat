import { afterEach, describe, expect, it } from 'vitest'
import { errorText, parseError } from './api'
import { locale } from './i18n'
import { failureText, rdpFailure } from './rdperror'

afterEach(() => locale.set('es'))

const wire = (code: string, data: Record<string, string>) => 'OSAT:' + JSON.stringify({ code, message: 'mensaje de Go', data })

// Lo que manda Go (rdgateway.Error.Data y rdpError) para cada paso que puede fallar.
const gw = { gateway: 'gw.corp:443' }
const cases: { name: string; code: string; data: Record<string, string>; vpn?: boolean }[] = [
  { name: 'nombre', code: 'GATEWAY', data: { ...gw, kind: 'dns', step: 'dns' }, vpn: true },
  { name: 'TCP sin respuesta', code: 'GATEWAY', data: { ...gw, kind: 'unreachable', reason: 'timeout', step: 'tcp' }, vpn: true },
  { name: 'TCP rechazado', code: 'GATEWAY', data: { ...gw, kind: 'unreachable', reason: 'refused', step: 'tcp' } },
  { name: 'TCP otro', code: 'GATEWAY', data: { ...gw, kind: 'unreachable', step: 'tcp' }, vpn: true },
  { name: 'TLS rechazado', code: 'GATEWAY', data: { ...gw, kind: 'tls', reason: 'rejected', step: 'tls' } },
  { name: 'TLS cortado', code: 'GATEWAY', data: { ...gw, kind: 'tls', reason: 'closed', step: 'tls' } },
  { name: 'TLS a tiempo', code: 'GATEWAY', data: { ...gw, kind: 'tls', reason: 'timeout', step: 'tls' } },
  { name: 'TLS otro', code: 'GATEWAY', data: { ...gw, kind: 'tls', step: 'tls' } },
  { name: 'HTTP', code: 'GATEWAY', data: { ...gw, kind: 'http', step: 'http', status: '403' } },
  { name: 'sin respuesta al primer pedido', code: 'GATEWAY', data: { ...gw, kind: 'connectionLost', reason: 'noAnswer', step: 'http' } },
  { name: 'sin NTLM', code: 'GATEWAY', data: { ...gw, kind: 'noNtlm', step: 'http' } },
  { name: 'contraseña', code: 'AUTH_FAILED', data: { ...gw, kind: 'gateway', step: 'auth', status: '401' } },
  { name: 'cortado', code: 'GATEWAY', data: { ...gw, kind: 'connectionLost', step: 'auth' } },
  { name: 'dejó de contestar', code: 'GATEWAY', data: { ...gw, kind: 'connectionLost', reason: 'timeout', step: 'handshake' } },
  { name: 'protocolo', code: 'GATEWAY', data: { ...gw, kind: 'protocol', step: 'handshake' } },
  { name: 'CAP', code: 'GATEWAY', data: { ...gw, kind: 'notAuthorized', reason: 'cap', step: 'tunnelAuth', code: '0x800759DB' } },
  { name: 'doble factor cortado', code: 'GATEWAY', data: { ...gw, kind: 'notAuthorized', reason: 'dropped', step: 'tunnelAuth' } },
  { name: 'otro código al autorizar', code: 'GATEWAY', data: { ...gw, kind: 'notAuthorized', step: 'tunnel', code: '0x800759E6' } },
  { name: 'doble factor a tiempo', code: 'GATEWAY', data: { ...gw, kind: 'timeout', step: 'tunnelAuth' } },
  { name: 'RAP', code: 'GATEWAY', data: { ...gw, kind: 'resourceDenied', step: 'channel', target: 'srv:3389', code: '0x800759DA' } },
  { name: 'el gateway no llega', code: 'GATEWAY', data: { ...gw, kind: 'targetUnreachable', reason: 'connect', step: 'channel', target: 'srv:3389', code: '0x800759DD' } },
  { name: 'el gateway no contesta el canal', code: 'GATEWAY', data: { ...gw, kind: 'targetUnreachable', reason: 'timeout', step: 'channel', target: 'srv:3389' } },
  { name: 'otro código del canal', code: 'GATEWAY', data: { ...gw, kind: 'targetUnreachable', step: 'channel', target: 'srv:3389', code: '0x800759D8' } },
  { name: 'cortado en el canal', code: 'GATEWAY', data: { ...gw, kind: 'connectionLost', reason: 'channel', step: 'channel', target: 'srv:3389' } },
  { name: 'X.224 con el equipo', code: 'RDP_SERVER', data: { ...gw, kind: 'x224', step: 'x224', host: 'srv:3389' } },
  { name: 'TLS con el equipo', code: 'RDP_SERVER', data: { ...gw, kind: 'tls', step: 'tls', host: 'srv:3389' } },
]

describe('errores del gateway', () => {
  for (const loc of ['es', 'en'] as const) {
    it(`cada paso tiene su propio mensaje y su paso (${loc})`, () => {
      locale.set(loc)
      const seen = new Map<string, string>()
      for (const c of cases) {
        const e = wire(c.code, c.data)
        const text = errorText(e)
        expect(text, c.name).not.toBe('mensaje de Go')
        expect(text, c.name).not.toMatch(/\{\w+\}/) // ninguna variable sin reemplazar
        expect(seen.get(text), `${c.name} repite el mensaje de ${seen.get(text)}`).toBeUndefined()
        seen.set(text, c.name)
        expect(text.includes('VPN'), `${c.name}: ${text}`).toBe(!!c.vpn)
        const f = rdpFailure(parseError(e))!
        expect(f.step, c.name).not.toBe(c.data.step) // traducido
      }
    })
  }

  it('detalles técnicos copiables, con el código del gateway', () => {
    const k = parseError(wire('GATEWAY', { ...gw, kind: 'notAuthorized', reason: 'cap', step: 'tunnelAuth', code: '0x800759DB', codeName: 'E_PROXY_NAP_ACCESSDENIED' }))
    const f = rdpFailure(k)!
    expect(f.step).toBe('Autorización del ingreso en el gateway')
    expect(failureText('No autorizado.', f)).toBe(
      [
        'No autorizado.',
        'Código de error: GATEWAY / notAuthorized / cap',
        'Paso: Autorización del ingreso en el gateway (tunnelAuth)',
        'Gateway: gw.corp:443',
        'Código del gateway: 0x800759DB (E_PROXY_NAP_ACCESSDENIED)',
      ].join('\n'),
    )
  })

  it('sin paso no hay detalles', () => {
    expect(rdpFailure(parseError(wire('NEED_SECRET', { kind: 'password' })))).toBeNull()
    expect(rdpFailure(null)).toBeNull()
  })
})
