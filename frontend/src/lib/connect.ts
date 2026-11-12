import { get } from 'svelte/store'
import { api, codedText, parseError, type OsatError, type SessionInfo } from './api'
import { askSecret, confirmDialog } from './dialog'
import { tr } from './i18n'
import { NeedsUserError } from './reconnect'
import { connections, LOCAL, reloadConnections, type Tab } from './stores'

export class CancelledError extends Error {
  constructor() {
    super(tr('connect.cancelled'))
  }
}

export interface ConnectOptions {
  /** false: no abre ningún diálogo (reconexión automática); lo que haría falta
   * preguntar se lanza como NeedsUserError. Por defecto true. */
  interactive?: boolean
  /** La terminal reconecta sobre lo que ya hay en pantalla: el backend no la borra. */
  reconnect?: boolean
}

/**
 * Abre la sesión de una pestaña resolviendo de forma interactiva lo que haga
 * falta: contraseña/passphrase, confirmación de huella del servidor, etc.
 */
export async function connectTab(tab: Tab, cols = 120, rows = 32, opts: ConnectOptions = {}): Promise<SessionInfo> {
  const interactive = opts.interactive ?? true
  // Terminal de este equipo: no hay servidor, contraseña ni huella que resolver.
  if (tab.kind === 'terminal' && tab.connId === LOCAL) {
    return api.openLocalTerminal(tab.shell ?? '', tab.initialDir ?? '', cols, rows)
  }
  if (tab.fromSession) {
    try {
      if (tab.kind === 'terminal') return await api.openTerminalFrom(tab.fromSession, cols, rows)
      return tab.kind === 'ports' ? await api.openTunnelFrom(tab.fromSession) : await api.openSFTPFrom(tab.fromSession)
    } catch {
      // La sesión origen pudo cerrarse: seguimos con una conexión nueva.
    }
  }

  const conn = get(connections).find((c) => c.id === tab.connId)
  if (!conn) throw interactive ? new Error(tr('connect.gone')) : new NeedsUserError(tr('connect.gone'))

  let secret = ''
  let saveSecret = false
  let acceptAfterForget = ''
  // Contraseñas de los saltos (ProxyJump) pedidas en el momento, por id de conexión.
  const jumpSecrets: JumpSecrets = {}

  // Con saltos hay una ronda de huella y contraseña por cada uno.
  for (let attempt = 0; attempt < 12; attempt++) {
    try {
      const req = {
        connectionId: conn.id,
        secret,
        saveSecret,
        cols,
        rows,
        jumpSecrets,
        ...(interactive ? {} : { noPrompt: true }),
        ...(opts.reconnect ? { reconnect: true } : {}),
      }
      const info =
        tab.kind === 'terminal' ? await api.openTerminal(req) : tab.kind === 'ports' ? await api.openTunnel(req) : await api.openSFTP(req)
      if (saveSecret || Object.values(jumpSecrets).some((j) => j.save)) reloadConnections()
      return info
    } catch (e) {
      const ke = parseError(e)
      if (!ke) throw e
      const d = ke.data ?? {}
      // Sin diálogos: lo que hay que preguntar o confirmar queda para el usuario.
      // CONN_GONE: el backend ya no tiene la conexión o la de su salto; reintentar no la va a traer.
      if (!interactive && ['NEED_SECRET', 'AUTH_FAILED', 'HOSTKEY_UNKNOWN', 'HOSTKEY_MISMATCH', 'CONN_GONE'].includes(ke.code)) {
        throw new NeedsUserError(codedText(ke))
      }

      switch (ke.code) {
        case 'CANCELLED':
          throw new CancelledError()

        case 'NEED_SECRET':
        case 'AUTH_FAILED': {
          if (d.kind === 'agent' || d.kind === 'key') throw new Error(codedText(ke))
          const isPass = d.kind === 'passphrase'
          // d.connId: es la de un salto por el que pasa la conexión, no la de la pestaña.
          const target = d.connId ? get(connections).find((c) => c.id === d.connId) : conn
          const r = await askSecret({
            title: ke.code === 'AUTH_FAILED' ? tr('connect.authFailed') : tr('rdp.connectTo', { title: d.name ?? conn.name }),
            message:
              ke.code === 'AUTH_FAILED'
                ? tr('connect.authFailedMsg', { text: codedText(ke) })
                : target
                  ? d.connId
                    ? tr('connect.jumpTarget', { target: `${target.user}@${target.host}:${target.port}` })
                    : `${target.user}@${target.host}:${target.port}`
                  : codedText(ke),
            label: isPass ? tr('connect.passphraseLabel') : tr('import.password'),
          })
          if (!r) throw new CancelledError()
          if (d.connId) {
            jumpSecrets[d.connId] = { secret: r.secret, save: r.save }
          } else {
            secret = r.secret
            saveSecret = r.save
          }
          break
        }

        case 'HOSTKEY_UNKNOWN': {
          if (acceptAfterForget && acceptAfterForget === d.fingerprint) {
            await api.trustHostKey(d.host)
            break
          }
          if (!(await confirmHostKey(ke))) throw new CancelledError()
          await api.trustHostKey(d.host)
          break
        }

        case 'HOSTKEY_MISMATCH': {
          if (!(await confirmHostKey(ke))) throw new CancelledError()
          await api.forgetHostKey(d.host)
          acceptAfterForget = d.fingerprint
          break
        }

        default:
          throw new Error(codedText(ke))
      }
    }
  }
  throw new Error(tr('connect.tooMany'))
}

/** Pregunta si confiar en la huella SSH de un servidor nuevo (HOSTKEY_UNKNOWN) o que cambió (HOSTKEY_MISMATCH). */
function confirmHostKey(ke: OsatError): Promise<boolean> {
  const d = ke.data ?? {}
  if (ke.code === 'HOSTKEY_MISMATCH') {
    return confirmDialog({
      title: tr('connect.hostChangedTitle'),
      message: tr('connect.hostChangedMsg', { text: codedText(ke) }),
      details: [
        ['Host', d.host],
        [tr('connect.hostExpected'), d.expected],
        [tr('connect.hostReceived'), d.fingerprint],
      ],
      okText: tr('connect.hostReplace'),
      danger: true,
    })
  }
  return confirmDialog({
    title: tr('connect.hostUnknownTitle'),
    message: tr('connect.hostUnknownMsg'),
    details: [
      ['Host', d.host],
      [tr('connect.hostType'), d.keyType],
      [tr('connect.hostFingerprint'), d.fingerprint],
    ],
    okText: tr('rdp.trustConnect'),
  })
}

export type JumpSecrets = Record<string, { secret: string; save: boolean }>

/**
 * Resuelve lo que pide el salto (ProxyJump) de una conexión RDP: confirmar su
 * huella SSH o su contraseña, que queda en jumpSecrets para el reintento.
 * null = el error no es de un salto (o no se puede resolver preguntando).
 */
export async function resolveJumpError(e: unknown, jumpSecrets: JumpSecrets): Promise<'retry' | 'cancel' | null> {
  const ke = parseError(e)
  const d = ke?.data ?? {}
  if (!ke || !d.connId) return null
  switch (ke.code) {
    case 'NEED_SECRET':
    case 'AUTH_FAILED': {
      if (d.kind === 'agent' || d.kind === 'key') return null
      const j = get(connections).find((c) => c.id === d.connId)
      const r = await askSecret({
        title: ke.code === 'AUTH_FAILED' ? tr('connect.authFailed') : tr('rdp.connectTo', { title: d.name }),
        message: ke.code === 'AUTH_FAILED' ? tr('connect.authFailedMsg', { text: codedText(ke) }) : j ? tr('connect.jumpTarget', { target: `${j.user}@${j.host}:${j.port}` }) : codedText(ke),
        label: d.kind === 'passphrase' ? tr('connect.passphraseLabel') : tr('import.password'),
      })
      if (!r) return 'cancel'
      jumpSecrets[d.connId] = { secret: r.secret, save: r.save }
      return 'retry'
    }
    case 'HOSTKEY_UNKNOWN':
    case 'HOSTKEY_MISMATCH':
      if (!(await confirmHostKey(ke))) return 'cancel'
      // Tras un cambio, la clave nueva quedó pendiente en el backend: se borra la vieja y se confía en esa.
      if (ke.code === 'HOSTKEY_MISMATCH') await api.forgetHostKey(d.host)
      await api.trustHostKey(d.host)
      return 'retry'
  }
  return null
}
