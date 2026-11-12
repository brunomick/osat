import { afterEach, describe, expect, it } from 'vitest'
import { codedText, errorText } from './api'
import { locale } from './i18n'

afterEach(() => locale.set('es'))

// Cada error con código que manda Go: el mensaje en español (el mismo que manda Go) y su versión en inglés.
const cases: { name: string; code: string; data?: Record<string, string>; es: string; en: string }[] = [
  { name: 'passphrase pedida', code: 'NEED_SECRET', data: { kind: 'passphrase' }, es: 'La clave privada está protegida. Ingresá la passphrase.', en: 'The private key is protected. Enter the passphrase.' },
  { name: 'contraseña pedida', code: 'NEED_SECRET', data: { kind: 'password' }, es: 'Ingresá la contraseña.', en: 'Enter the password.' },
  { name: 'datos adicionales', code: 'NEED_SECRET', data: { kind: 'prompt' }, es: 'El servidor pide datos adicionales para entrar.', en: 'The server asks for additional information to log in.' },
  { name: 'maestra que falta', code: 'NEED_SECRET', data: { kind: 'master', reason: 'missing' }, es: 'las contraseñas están protegidas con una contraseña maestra.', en: 'the passwords are protected with a master password.' },
  { name: 'maestra incorrecta', code: 'NEED_SECRET', data: { kind: 'master', reason: 'bad' }, es: 'la contraseña maestra es incorrecta.', en: 'the master password is wrong.' },
  { name: 'passphrase incorrecta', code: 'AUTH_FAILED', data: { kind: 'passphrase' }, es: 'Passphrase incorrecta.', en: 'Wrong passphrase.' },
  { name: 'autenticación rechazada', code: 'AUTH_FAILED', data: { kind: 'password' }, es: 'Autenticación rechazada por el servidor.', en: 'Authentication rejected by the server.' },
  { name: 'host desconocido', code: 'HOSTKEY_UNKNOWN', es: 'Servidor desconocido: verificá la huella antes de continuar.', en: 'Unknown server: check the fingerprint before continuing.' },
  { name: 'huella cambiada', code: 'HOSTKEY_MISMATCH', es: '¡ATENCIÓN! La clave del servidor cambió. Podría tratarse de un ataque man-in-the-middle.', en: 'WARNING! The server key changed. This could be a man-in-the-middle attack.' },
  { name: 'conflicto de un nombre', code: 'CONFLICT', data: { kind: 'name' }, es: 'Ya existe un elemento con ese nombre en la carpeta de destino.', en: 'An item with that name already exists in the destination folder.' },
  { name: 'conflicto de varios', code: 'CONFLICT', data: { kind: 'items' }, es: 'Algunos elementos ya existen en el destino.', en: 'Some items already exist at the destination.' },
  { name: 'cancelado', code: 'CANCELLED', es: 'Autenticación cancelada.', en: 'Authentication cancelled.' },
  { name: 'conexión inexistente', code: 'CONN_GONE', es: 'conexión inexistente', en: 'connection does not exist' },
  { name: 'salto borrado', code: 'CONN_GONE', data: { kind: 'jump', name: 'bastión' }, es: 'la conexión de salto de «bastión» ya no existe', en: 'the jump connection of «bastión» no longer exists' },
  { name: 'sin papelera', code: 'NO_TRASH', es: 'No se puede enviar a la papelera.', en: 'Can\'t be moved to the trash.' },
  { name: 'certificado RDP desconocido', code: 'RDPCERT_UNKNOWN', es: 'El certificado del servidor no es de confianza para el sistema: verificá la huella antes de continuar.', en: 'The server certificate is not trusted by the system: check the fingerprint before continuing.' },
  { name: 'certificado RDP cambiado', code: 'RDPCERT_MISMATCH', es: '¡ATENCIÓN! El certificado del servidor cambió. Podría tratarse de un ataque man-in-the-middle.', en: 'WARNING! The server certificate changed. This could be a man-in-the-middle attack.' },
  { name: 'permiso para leer', code: 'PERMISSION', data: { kind: 'read' }, es: 'No tenés permiso para leer este archivo.', en: 'You don\'t have permission to read this file.' },
  { name: 'permiso para guardar', code: 'PERMISSION', data: { kind: 'save' }, es: 'No tenés permiso para guardar este archivo.', en: 'You don\'t have permission to save this file.' },
  { name: 'sudo pide contraseña', code: 'SUDO_PASSWORD', es: 'sudo pide la contraseña.', en: 'sudo is asking for the password.' },
  { name: 'contraseña de sudo incorrecta', code: 'SUDO_BAD_PASSWORD', es: 'Contraseña de sudo incorrecta.', en: 'Wrong sudo password.' },
  { name: 'servidor cortado', code: 'SERVER_GONE', es: 'se cortó la conexión con el servidor', en: 'the connection to the server was lost' },
]

// Como llega de Wails: un string "OSAT:{json}" con un mensaje de Go que el frontend no usa si conoce el código.
const wire = (code: string, data?: Record<string, string>, message = 'mensaje de Go que no se muestra') => 'OSAT:' + JSON.stringify({ code, message, data })

describe('errores con código', () => {
  for (const c of cases) {
    it(`${c.name} en español y en inglés`, () => {
      expect(errorText(wire(c.code, c.data))).toBe(c.es)
      locale.set('en')
      expect(errorText(wire(c.code, c.data))).toBe(c.en)
    })
  }

  it('el prefijo del salto se traduce', () => {
    const e = wire('AUTH_FAILED', { kind: 'password', jump: 'bastión', name: 'bastión', connId: 'j1' })
    expect(errorText(e)).toBe('Salto «bastión»: Autenticación rechazada por el servidor.')
    locale.set('en')
    expect(errorText(e)).toBe('Jump host «bastión»: Authentication rejected by the server.')
  })

  it('un código sin traducción muestra el mensaje de Go', () => {
    expect(errorText(wire('OTRO', undefined, 'texto de Go'))).toBe('texto de Go')
    expect(codedText({ code: 'NEED_SECRET', message: 'texto de Go', data: { kind: 'otro' } } as never)).toBe('texto de Go')
  })

  it('un error sin código se muestra tal cual', () => {
    expect(errorText(new Error('boom'))).toBe('boom')
  })
})
