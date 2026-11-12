// Lógica de la pestaña VNC que no depende de noVNC ni del DOM (para poder probarla).

/** ¿El servidor pide solo la contraseña VNC? Si pide también usuario u otra cosa (RealVNC, Apple, Plain...), osat no lo soporta. */
export const onlyPassword = (types: string[]) => types.length === 1 && types[0] === 'password'

/** Lo que se sabe de la conexión cuando noVNC avisa que terminó. */
export interface VncFacts {
  /** Llegó a conectar (noVNC avisó "connect"). */
  connected: boolean
  /** noVNC lo da por cierre limpio: false si se cortó o falló. */
  clean: boolean
  /** El usuario canceló el pedido de contraseña. */
  cancelled: boolean
  /** El servidor pidió una autenticación que osat no soporta. */
  unsupportedAuth: boolean
  /** El servidor rechazó la autenticación (noVNC "securityfailure"), con el motivo que dio, si dio. */
  securityFailure: { reason: string } | null
  /** Se le mandó una contraseña al servidor. */
  passwordSent: boolean
}

export type VncEnd =
  | { kind: 'cancelled' | 'unsupportedAuth' | 'handshake' | 'lost' | 'closed' }
  | { kind: 'badPassword' | 'refused'; reason: string }

/**
 * Por qué terminó la conexión, para decirle al usuario qué hacer.
 * noVNC no avisa cuando el servidor usa un cifrado o una autenticación que no
 * soporta (VeNCrypt con TLS, por ejemplo): solo corta antes de conectar. Eso es
 * "handshake".
 */
export function classifyEnd(f: VncFacts): VncEnd {
  if (f.cancelled) return { kind: 'cancelled' }
  if (f.unsupportedAuth) return { kind: 'unsupportedAuth' }
  if (f.securityFailure) return { kind: f.passwordSent ? 'badPassword' : 'refused', reason: f.securityFailure.reason }
  if (!f.connected) return { kind: 'handshake' }
  return { kind: f.clean ? 'closed' : 'lost' }
}
