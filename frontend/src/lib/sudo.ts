import { parseError } from './api'
import { askSecret, confirmDialog } from './dialog'
import { tr } from './i18n'

// Editar como root con sudo (ver internal/remote/sudo.go).

/** El backend no pudo leer o guardar por SFTP por falta de permiso (se puede ofrecer sudo). */
export const isPermissionError = (e: unknown) => parseError(e)?.code === 'PERMISSION'

/** Pregunta si hacerlo como root después de un permiso denegado. */
export function askRoot(what: 'read' | 'save') {
  return confirmDialog({
    title: tr('sudo.deniedTitle'),
    message:
      what === 'read'
        ? tr('sudo.askRead')
        : tr('sudo.askSave'),
    okText: what === 'read' ? tr('sudo.openRoot') : tr('sudo.saveRoot'),
  })
}

/**
 * Corre una operación como root. Si sudo pide la contraseña (o la que se dio no sirvió), la pregunta,
 * sin opción de guardarla, y reintenta. Si se cancela, tira el último error.
 * La primera vez va sin contraseña: el backend prueba sin ella (NOPASSWD) o con la que recuerda.
 */
export async function withSudoPassword<T>(call: (password: string) => Promise<T>): Promise<T> {
  let password = ''
  for (;;) {
    try {
      return await call(password)
    } catch (e) {
      const code = parseError(e)?.code
      if (code !== 'SUDO_PASSWORD' && code !== 'SUDO_BAD_PASSWORD') throw e
      const r = await askSecret({
        title: tr('sudo.passwordTitle'),
        message: code === 'SUDO_BAD_PASSWORD' ? tr('sudo.badPassword') : tr('sudo.needsPassword'),
        label: tr('import.password'),
        allowSave: false,
        okText: tr('common.accept'),
      })
      if (!r) throw e
      password = r.secret
    }
  }
}
