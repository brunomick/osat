import type { Connection, Protocol } from './api'

/** Puerto por defecto de cada protocolo. */
export const defaultPort: Record<Protocol, number> = { ssh: 22, rdp: 3389, vnc: 5900, mongo: 27017 }

/** Las conexiones SSH viejas pueden venir sin protocolo. */
export const protocolOf = (c: Pick<Connection, 'protocol'>): Protocol => c.protocol ?? 'ssh'

/** Solo se pasa "a través de" una conexión SSH (Proxy Jump). */
export const canBeJump = (c: Pick<Connection, 'protocol'>) => protocolOf(c) === 'ssh'

/** Conexión MongoDB: se abre con mongosh en una terminal, no tiene SFTP ni reenvíos. */
export const isMongo = (c: Pick<Connection, 'protocol'> | undefined) => c?.protocol === 'mongo'

/** Qué campos del editor de conexiones aplican a cada protocolo. */
export function formFields(p: Protocol) {
  return {
    uri: p === 'mongo', // MongoDB: la cadena de conexión reemplaza host, puerto y usuario
    user: p !== 'vnc' && p !== 'mongo', // VNC solo tiene contraseña
    domain: p === 'rdp',
    gateway: p === 'rdp',
    auth: p === 'ssh', // contraseña, clave o agente
    advanced: p === 'ssh', // reenvío del agente, comando inicial, variables
    defaults: p !== 'mongo', // las credenciales por defecto son de los servidores
  }
}
