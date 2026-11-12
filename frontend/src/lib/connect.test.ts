import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Connection } from './api'
import type { Tab } from './stores'

// Todo lo que toca el backend o la interfaz se simula: acá se prueba solo el flujo de connect.ts.
const m = vi.hoisted(() => ({
  openTerminal: vi.fn(),
  openTunnel: vi.fn(),
  openSFTP: vi.fn(),
  openLocalTerminal: vi.fn(),
  openTerminalFrom: vi.fn(),
  openTunnelFrom: vi.fn(),
  openSFTPFrom: vi.fn(),
  trustHostKey: vi.fn(),
  forgetHostKey: vi.fn(),
  askSecret: vi.fn(),
  confirmDialog: vi.fn(),
  reloadConnections: vi.fn(),
}))

vi.mock('./api', async () => {
  // parseError es el real: así los errores de prueba pasan por el mismo camino "OSAT:{json}" que los del backend.
  const actual = await vi.importActual<typeof import('./api')>('./api')
  return {
    parseError: actual.parseError,
    codedText: actual.codedText,
    api: {
      openTerminal: m.openTerminal,
      openTunnel: m.openTunnel,
      openSFTP: m.openSFTP,
      openLocalTerminal: m.openLocalTerminal,
      openTerminalFrom: m.openTerminalFrom,
      openTunnelFrom: m.openTunnelFrom,
      openSFTPFrom: m.openSFTPFrom,
      trustHostKey: m.trustHostKey,
      forgetHostKey: m.forgetHostKey,
    },
  }
})
vi.mock('./dialog', () => ({ askSecret: m.askSecret, confirmDialog: m.confirmDialog }))
// stores.ts lee localStorage al cargarse: se reemplaza entero.
vi.mock('./stores', async () => {
  const { writable } = await import('svelte/store')
  return { connections: writable([]), reloadConnections: m.reloadConnections, LOCAL: 'local' }
})

import { CancelledError, connectTab, resolveJumpError, type JumpSecrets } from './connect'
import { NeedsUserError } from './reconnect'
import { connections } from './stores'

const conn = (id: string, extra: Partial<Connection> = {}): Connection => ({
  id,
  name: id,
  group: '',
  host: `${id}.example.com`,
  port: 22,
  user: 'root',
  authType: 'password',
  hasSecret: false,
  ...extra,
})
const target = conn('srv')
const jump = conn('jump', { name: 'Bastión', user: 'ops', host: 'bastion.example.com', port: 2222 })

const tab = (extra: Partial<Tab> = {}): Tab => ({ id: 't1', kind: 'terminal', connId: 'srv', title: 'srv', status: 'connecting', ...extra })
const info = { id: 's1', kind: 'terminal' as const, connId: 'srv', title: 'srv' }

// Error como lo emite el backend: "OSAT:{json}".
const osatError = (code: string, message: string, data?: Record<string, string>) =>
  new Error('OSAT:' + JSON.stringify({ code, message, data }))
const needSecret = (data?: Record<string, string>) => osatError('NEED_SECRET', 'Hace falta la contraseña.', data)
const hostKeyUnknown = (fingerprint = 'SHA256:aaa') =>
  osatError('HOSTKEY_UNKNOWN', 'Servidor desconocido.', { host: 'srv.example.com:22', keyType: 'ssh-ed25519', fingerprint })
const hostKeyMismatch = (fingerprint = 'SHA256:bbb') =>
  osatError('HOSTKEY_MISMATCH', 'La clave del servidor cambió.', {
    host: 'srv.example.com:22',
    expected: 'SHA256:old',
    fingerprint,
  })

beforeEach(() => {
  for (const f of Object.values(m)) f.mockReset()
  connections.set([target, jump])
})

describe('connectTab', () => {
  describe('conexión directa', () => {
    it('funciona a la primera y no pide nada', async () => {
      m.openTerminal.mockResolvedValue(info)
      await expect(connectTab(tab(), 100, 40)).resolves.toBe(info)
      expect(m.openTerminal).toHaveBeenCalledTimes(1)
      expect(m.openTerminal).toHaveBeenCalledWith({
        connectionId: 'srv',
        secret: '',
        saveSecret: false,
        cols: 100,
        rows: 40,
        jumpSecrets: {},
      })
      expect(m.askSecret).not.toHaveBeenCalled()
      expect(m.confirmDialog).not.toHaveBeenCalled()
      expect(m.reloadConnections).not.toHaveBeenCalled()
    })

    it('usa el tamaño por defecto 120x32', async () => {
      m.openTerminal.mockResolvedValue(info)
      await connectTab(tab())
      expect(m.openTerminal).toHaveBeenCalledWith(expect.objectContaining({ cols: 120, rows: 32 }))
    })

    it('según el tipo de pestaña abre SFTP o el túnel de puertos', async () => {
      m.openSFTP.mockResolvedValue({ ...info, kind: 'sftp' })
      m.openTunnel.mockResolvedValue({ ...info, kind: 'tunnel' })
      await connectTab(tab({ kind: 'sftp' }))
      await connectTab(tab({ kind: 'ports' }))
      expect(m.openSFTP).toHaveBeenCalledTimes(1)
      expect(m.openTunnel).toHaveBeenCalledTimes(1)
      expect(m.openTerminal).not.toHaveBeenCalled()
    })

    it('una conexión que ya no existe falla sin llamar al backend', async () => {
      await expect(connectTab(tab({ connId: 'borrada' }))).rejects.toThrow('La conexión ya no existe')
      expect(m.openTerminal).not.toHaveBeenCalled()
    })
  })

  describe('terminal de este equipo y sesión de origen', () => {
    it('una terminal local abre la shell sin resolver nada', async () => {
      m.openLocalTerminal.mockResolvedValue(info)
      await connectTab(tab({ connId: 'local', shell: 'bash', initialDir: '/tmp' }), 90, 30)
      expect(m.openLocalTerminal).toHaveBeenCalledWith('bash', '/tmp', 90, 30)
      expect(m.openTerminal).not.toHaveBeenCalled()
    })

    it('con fromSession reutiliza esa conexión', async () => {
      m.openTerminalFrom.mockResolvedValue(info)
      await connectTab(tab({ fromSession: 'sess-1' }), 80, 24)
      expect(m.openTerminalFrom).toHaveBeenCalledWith('sess-1', 80, 24)
      expect(m.openTerminal).not.toHaveBeenCalled()
    })

    it('si la sesión de origen ya no está, sigue con una conexión nueva', async () => {
      m.openSFTPFrom.mockRejectedValue(new Error('sesión cerrada'))
      m.openSFTP.mockResolvedValue({ ...info, kind: 'sftp' })
      await connectTab(tab({ kind: 'sftp', fromSession: 'sess-1' }))
      expect(m.openSFTPFrom).toHaveBeenCalledWith('sess-1')
      expect(m.openSFTP).toHaveBeenCalledTimes(1)
    })
  })

  describe('contraseña (NEED_SECRET)', () => {
    it('pide la contraseña y reintenta con ese secreto', async () => {
      m.openTerminal.mockRejectedValueOnce(needSecret()).mockResolvedValueOnce(info)
      m.askSecret.mockResolvedValue({ secret: 'hunter2', save: false })
      await expect(connectTab(tab())).resolves.toBe(info)
      expect(m.askSecret).toHaveBeenCalledTimes(1)
      expect(m.askSecret).toHaveBeenCalledWith(
        expect.objectContaining({ title: 'Conectar a srv', message: 'root@srv.example.com:22', label: 'Contraseña' }),
      )
      expect(m.openTerminal).toHaveBeenCalledTimes(2)
      expect(m.openTerminal).toHaveBeenLastCalledWith(expect.objectContaining({ secret: 'hunter2', saveSecret: false }))
      expect(m.reloadConnections).not.toHaveBeenCalled()
    })

    it('si el usuario eligió guardarla, la manda con saveSecret y recarga las conexiones', async () => {
      m.openTerminal.mockRejectedValueOnce(needSecret()).mockResolvedValueOnce(info)
      m.askSecret.mockResolvedValue({ secret: 'hunter2', save: true })
      await connectTab(tab())
      expect(m.openTerminal).toHaveBeenLastCalledWith(expect.objectContaining({ secret: 'hunter2', saveSecret: true }))
      expect(m.reloadConnections).toHaveBeenCalledTimes(1)
    })

    it('pide la passphrase cuando el error es de una clave', async () => {
      m.openTerminal.mockRejectedValueOnce(needSecret({ kind: 'passphrase' })).mockResolvedValueOnce(info)
      m.askSecret.mockResolvedValue({ secret: 'frase', save: false })
      await connectTab(tab())
      expect(m.askSecret).toHaveBeenCalledWith(expect.objectContaining({ label: 'Passphrase de la clave' }))
    })

    it('si se cancela el diálogo lanza CancelledError y no reintenta', async () => {
      m.openTerminal.mockRejectedValue(needSecret())
      m.askSecret.mockResolvedValue(null)
      await expect(connectTab(tab())).rejects.toBeInstanceOf(CancelledError)
      expect(m.openTerminal).toHaveBeenCalledTimes(1)
    })

    it('AUTH_FAILED vuelve a pedir la contraseña con el título de fallo', async () => {
      m.openTerminal.mockRejectedValueOnce(osatError('AUTH_FAILED', 'Contraseña incorrecta.')).mockResolvedValueOnce(info)
      m.askSecret.mockResolvedValue({ secret: 'otra', save: false })
      await connectTab(tab())
      expect(m.askSecret).toHaveBeenCalledWith(
        expect.objectContaining({ title: 'Autenticación fallida', message: 'Autenticación rechazada por el servidor. Volvé a intentarlo.' }),
      )
    })

    it('con agente o clave no se pregunta nada: se muestra el mensaje del backend', async () => {
      m.openTerminal.mockRejectedValue(needSecret({ kind: 'agent' }))
      await expect(connectTab(tab())).rejects.toThrow('Hace falta la contraseña.')
      expect(m.askSecret).not.toHaveBeenCalled()
    })
  })

  describe('huella del servidor', () => {
    it('HOSTKEY_UNKNOWN aceptada: confía en el host y reintenta', async () => {
      m.openTerminal.mockRejectedValueOnce(hostKeyUnknown()).mockResolvedValueOnce(info)
      m.confirmDialog.mockResolvedValue(true)
      await expect(connectTab(tab())).resolves.toBe(info)
      expect(m.confirmDialog).toHaveBeenCalledTimes(1)
      expect(m.confirmDialog).toHaveBeenCalledWith(
        expect.objectContaining({
          title: 'Servidor desconocido',
          details: expect.arrayContaining([['Huella', 'SHA256:aaa']]),
        }),
      )
      expect(m.trustHostKey).toHaveBeenCalledWith('srv.example.com:22')
      expect(m.openTerminal).toHaveBeenCalledTimes(2)
    })

    it('HOSTKEY_UNKNOWN rechazada: CancelledError, no confía y no reintenta', async () => {
      m.openTerminal.mockRejectedValue(hostKeyUnknown())
      m.confirmDialog.mockResolvedValue(false)
      await expect(connectTab(tab())).rejects.toBeInstanceOf(CancelledError)
      expect(m.trustHostKey).not.toHaveBeenCalled()
      expect(m.openTerminal).toHaveBeenCalledTimes(1)
    })

    it('HOSTKEY_MISMATCH aceptada: olvida la clave vieja y acepta la misma huella nueva sin volver a preguntar', async () => {
      m.openTerminal
        .mockRejectedValueOnce(hostKeyMismatch('SHA256:bbb'))
        .mockRejectedValueOnce(hostKeyUnknown('SHA256:bbb'))
        .mockResolvedValueOnce(info)
      m.confirmDialog.mockResolvedValue(true)
      await expect(connectTab(tab())).resolves.toBe(info)
      expect(m.confirmDialog).toHaveBeenCalledTimes(1)
      expect(m.confirmDialog).toHaveBeenCalledWith(
        expect.objectContaining({ title: '⚠ La clave del servidor cambió', danger: true }),
      )
      expect(m.forgetHostKey).toHaveBeenCalledWith('srv.example.com:22')
      expect(m.trustHostKey).toHaveBeenCalledWith('srv.example.com:22')
      expect(m.forgetHostKey.mock.invocationCallOrder[0]).toBeLessThan(m.trustHostKey.mock.invocationCallOrder[0])
      expect(m.openTerminal).toHaveBeenCalledTimes(3)
    })

    it('HOSTKEY_MISMATCH rechazada: CancelledError y no toca el known_hosts', async () => {
      m.openTerminal.mockRejectedValue(hostKeyMismatch())
      m.confirmDialog.mockResolvedValue(false)
      await expect(connectTab(tab())).rejects.toBeInstanceOf(CancelledError)
      expect(m.forgetHostKey).not.toHaveBeenCalled()
      expect(m.trustHostKey).not.toHaveBeenCalled()
    })

    it('si tras olvidar la clave llega otra huella distinta, vuelve a preguntar', async () => {
      m.openTerminal
        .mockRejectedValueOnce(hostKeyMismatch('SHA256:bbb'))
        .mockRejectedValueOnce(hostKeyUnknown('SHA256:ccc'))
        .mockResolvedValueOnce(info)
      m.confirmDialog.mockResolvedValue(true)
      await connectTab(tab())
      expect(m.confirmDialog).toHaveBeenCalledTimes(2)
    })
  })

  describe('salto (ProxyJump)', () => {
    it('la contraseña de un salto va a jumpSecrets y no al secreto de la conexión', async () => {
      m.openTerminal.mockRejectedValueOnce(needSecret({ connId: 'jump', name: 'Bastión' })).mockResolvedValueOnce(info)
      m.askSecret.mockResolvedValue({ secret: 'clave-del-salto', save: false })
      await expect(connectTab(tab())).resolves.toBe(info)
      expect(m.askSecret).toHaveBeenCalledWith(
        expect.objectContaining({ title: 'Conectar a Bastión', message: 'Salto: ops@bastion.example.com:2222' }),
      )
      expect(m.openTerminal).toHaveBeenLastCalledWith(
        expect.objectContaining({ secret: '', saveSecret: false, jumpSecrets: { jump: { secret: 'clave-del-salto', save: false } } }),
      )
      expect(m.reloadConnections).not.toHaveBeenCalled()
    })

    it('si se guarda la contraseña del salto, recarga las conexiones', async () => {
      m.openTerminal.mockRejectedValueOnce(needSecret({ connId: 'jump', name: 'Bastión' })).mockResolvedValueOnce(info)
      m.askSecret.mockResolvedValue({ secret: 'clave-del-salto', save: true })
      await connectTab(tab())
      expect(m.reloadConnections).toHaveBeenCalledTimes(1)
    })

    it('cancelar la contraseña del salto lanza CancelledError', async () => {
      m.openTerminal.mockRejectedValue(needSecret({ connId: 'jump', name: 'Bastión' }))
      m.askSecret.mockResolvedValue(null)
      await expect(connectTab(tab())).rejects.toBeInstanceOf(CancelledError)
    })

    it('cadena con un salto: huella del salto, su contraseña y después la del servidor', async () => {
      m.openTerminal
        .mockRejectedValueOnce(hostKeyUnknown('SHA256:jump'))
        .mockRejectedValueOnce(needSecret({ connId: 'jump', name: 'Bastión' }))
        .mockRejectedValueOnce(needSecret())
        .mockResolvedValueOnce(info)
      m.confirmDialog.mockResolvedValue(true)
      m.askSecret
        .mockResolvedValueOnce({ secret: 'pw-salto', save: false })
        .mockResolvedValueOnce({ secret: 'pw-servidor', save: false })
      await expect(connectTab(tab())).resolves.toBe(info)
      expect(m.openTerminal).toHaveBeenLastCalledWith(
        expect.objectContaining({
          secret: 'pw-servidor',
          jumpSecrets: { jump: { secret: 'pw-salto', save: false } },
        }),
      )
    })
  })

  describe('otros errores', () => {
    it('un error que no es de osat se propaga tal cual', async () => {
      const boom = new Error('connection refused')
      m.openTerminal.mockRejectedValue(boom)
      await expect(connectTab(tab())).rejects.toBe(boom)
    })

    it('CANCELLED del backend es un CancelledError', async () => {
      m.openTerminal.mockRejectedValue(osatError('CANCELLED', 'Cancelado.'))
      await expect(connectTab(tab())).rejects.toBeInstanceOf(CancelledError)
    })

    it('un código que no se resuelve preguntando se muestra con su mensaje', async () => {
      m.openTerminal.mockRejectedValue(osatError('CONFLICT', 'Ya existe.'))
      await expect(connectTab(tab())).rejects.toThrow('Ya existe.')
    })

    it('se rinde después de 12 intentos', async () => {
      m.openTerminal.mockRejectedValue(needSecret())
      m.askSecret.mockResolvedValue({ secret: 'x', save: false })
      await expect(connectTab(tab())).rejects.toThrow('Demasiados intentos')
      expect(m.openTerminal).toHaveBeenCalledTimes(12)
    })
  })
})

describe('connectTab sin preguntas (interactive: false)', () => {
  const quiet = { interactive: false }

  it('le pide al backend que no pregunte nada', async () => {
    m.openTerminal.mockResolvedValue(info)
    await expect(connectTab(tab(), 80, 24, quiet)).resolves.toBe(info)
    expect(m.openTerminal).toHaveBeenCalledWith(expect.objectContaining({ noPrompt: true }))
    expect(m.openTerminal.mock.calls[0][0]).not.toHaveProperty('reconnect')
  })

  it('con reconnect avisa que la terminal no se borra', async () => {
    m.openTerminal.mockResolvedValue(info)
    await connectTab(tab(), 80, 24, { interactive: false, reconnect: true })
    expect(m.openTerminal).toHaveBeenCalledWith(expect.objectContaining({ noPrompt: true, reconnect: true }))
  })

  it.each([
    ['NEED_SECRET', needSecret()],
    ['NEED_SECRET de un salto', needSecret({ connId: 'jump', name: 'Bastión' })],
    ['AUTH_FAILED', osatError('AUTH_FAILED', 'Autenticación rechazada por el servidor.', { kind: 'key' })],
    ['HOSTKEY_UNKNOWN', hostKeyUnknown()],
    ['HOSTKEY_MISMATCH', hostKeyMismatch()],
    ['CONN_GONE', osatError('CONN_GONE', 'conexión inexistente', { connId: 'srv' })],
    ['CONN_GONE de un salto', osatError('CONN_GONE', 'la conexión de salto de «srv» ya no existe', { name: 'srv', jumpId: 'jump' })],
  ])('%s: NeedsUserError sin abrir diálogos ni reintentar', async (_name, err) => {
    m.openTerminal.mockRejectedValue(err)
    const p = connectTab(tab(), 80, 24, quiet)
    await expect(p).rejects.toBeInstanceOf(NeedsUserError)
    expect(m.openTerminal).toHaveBeenCalledTimes(1)
    expect(m.askSecret).not.toHaveBeenCalled()
    expect(m.confirmDialog).not.toHaveBeenCalled()
    expect(m.trustHostKey).not.toHaveBeenCalled()
    expect(m.forgetHostKey).not.toHaveBeenCalled()
  })

  it('CONN_GONE con diálogos se muestra con su mensaje, sin NeedsUserError', async () => {
    m.openTerminal.mockRejectedValue(osatError('CONN_GONE', 'conexión inexistente', { connId: 'srv' }))
    const p = connectTab(tab())
    await expect(p).rejects.toThrow('conexión inexistente')
    await expect(p).rejects.not.toBeInstanceOf(NeedsUserError)
  })

  it('una conexión que ya no existe es NeedsUserError', async () => {
    await expect(connectTab(tab({ connId: 'borrada' }), 80, 24, quiet)).rejects.toBeInstanceOf(NeedsUserError)
    expect(m.openTerminal).not.toHaveBeenCalled()
  })

  it('un error de red no es NeedsUserError: se puede reintentar', async () => {
    const boom = new Error('connection refused')
    m.openSFTP.mockRejectedValue(boom)
    const p = connectTab(tab({ kind: 'sftp' }), 80, 24, quiet)
    await expect(p).rejects.toBe(boom)
    await expect(p).rejects.not.toBeInstanceOf(NeedsUserError)
  })

  it('con la sesión de origen caída sigue con una conexión nueva, sin preguntas', async () => {
    m.openTerminalFrom.mockRejectedValue(new Error('la sesión no existe'))
    m.openTerminal.mockResolvedValue(info)
    await connectTab(tab({ fromSession: 'vieja' }), 80, 24, quiet)
    expect(m.openTerminal).toHaveBeenCalledWith(expect.objectContaining({ noPrompt: true }))
  })
})

describe('resolveJumpError', () => {
  it('devuelve null si el error no es de osat', async () => {
    const js: JumpSecrets = {}
    expect(await resolveJumpError(new Error('boom'), js)).toBeNull()
    expect(await resolveJumpError('texto suelto', js)).toBeNull()
  })

  it('devuelve null si el error no es de un salto (sin connId)', async () => {
    expect(await resolveJumpError(needSecret(), {})).toBeNull()
    expect(await resolveJumpError(hostKeyUnknown(), {})).toBeNull()
    expect(m.askSecret).not.toHaveBeenCalled()
    expect(m.confirmDialog).not.toHaveBeenCalled()
  })

  it('devuelve null si es de un salto pero no se resuelve preguntando', async () => {
    expect(await resolveJumpError(osatError('CONFLICT', 'x', { connId: 'jump' }), {})).toBeNull()
    expect(await resolveJumpError(needSecret({ connId: 'jump', kind: 'agent' }), {})).toBeNull()
    expect(await resolveJumpError(needSecret({ connId: 'jump', kind: 'key' }), {})).toBeNull()
    expect(m.askSecret).not.toHaveBeenCalled()
  })

  it('contraseña del salto: la guarda en jumpSecrets y pide reintentar', async () => {
    m.askSecret.mockResolvedValue({ secret: 'pw-salto', save: true })
    const js: JumpSecrets = {}
    expect(await resolveJumpError(needSecret({ connId: 'jump', name: 'Bastión' }), js)).toBe('retry')
    expect(js).toEqual({ jump: { secret: 'pw-salto', save: true } })
    expect(m.askSecret).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Conectar a Bastión', message: 'Salto: ops@bastion.example.com:2222' }),
    )
  })

  it('contraseña del salto cancelada: pide cancelar y no guarda nada', async () => {
    m.askSecret.mockResolvedValue(null)
    const js: JumpSecrets = {}
    expect(await resolveJumpError(needSecret({ connId: 'jump', name: 'Bastión' }), js)).toBe('cancel')
    expect(js).toEqual({})
  })

  it('huella desconocida del salto aceptada: confía y pide reintentar', async () => {
    m.confirmDialog.mockResolvedValue(true)
    const e = osatError('HOSTKEY_UNKNOWN', 'Servidor desconocido.', { connId: 'jump', host: 'bastion.example.com:2222', fingerprint: 'SHA256:j' })
    expect(await resolveJumpError(e, {})).toBe('retry')
    expect(m.trustHostKey).toHaveBeenCalledWith('bastion.example.com:2222')
    expect(m.forgetHostKey).not.toHaveBeenCalled()
  })

  it('huella del salto rechazada: pide cancelar sin tocar el known_hosts', async () => {
    m.confirmDialog.mockResolvedValue(false)
    const e = osatError('HOSTKEY_UNKNOWN', 'Servidor desconocido.', { connId: 'jump', host: 'bastion.example.com:2222' })
    expect(await resolveJumpError(e, {})).toBe('cancel')
    expect(m.trustHostKey).not.toHaveBeenCalled()
  })

  it('huella cambiada del salto aceptada: olvida la vieja, confía en la nueva y pide reintentar', async () => {
    m.confirmDialog.mockResolvedValue(true)
    const e = osatError('HOSTKEY_MISMATCH', 'Cambió.', { connId: 'jump', host: 'bastion.example.com:2222' })
    expect(await resolveJumpError(e, {})).toBe('retry')
    expect(m.forgetHostKey).toHaveBeenCalledWith('bastion.example.com:2222')
    expect(m.trustHostKey).toHaveBeenCalledWith('bastion.example.com:2222')
    expect(m.forgetHostKey.mock.invocationCallOrder[0]).toBeLessThan(m.trustHostKey.mock.invocationCallOrder[0])
  })

  it('huella cambiada del salto rechazada: pide cancelar', async () => {
    m.confirmDialog.mockResolvedValue(false)
    const e = osatError('HOSTKEY_MISMATCH', 'Cambió.', { connId: 'jump', host: 'bastion.example.com:2222' })
    expect(await resolveJumpError(e, {})).toBe('cancel')
    expect(m.forgetHostKey).not.toHaveBeenCalled()
    expect(m.trustHostKey).not.toHaveBeenCalled()
  })
})
