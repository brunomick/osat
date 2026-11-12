import type { LocalShell } from './api'

/** Dónde guardaba la app la "última terminal usada", antes de que fuera un ajuste. */
export const SHELL_KEY = 'osat.defaultShell'

/** La terminal que pasa a los ajustes: la que se venía usando (si sigue instalada) o, si no, la primera detectada. "" si no hay ninguna. */
export function shellToMigrate(stored: string | null, shells: LocalShell[]): string {
  if (stored && shells.some((s) => s.id === stored)) return stored
  return shells[0]?.id ?? ''
}

interface Migration {
  /** Terminal local por defecto que ya tienen los ajustes ("" = todavía ninguna). */
  current: string
  shells: LocalShell[]
  storage: Pick<Storage, 'getItem' | 'removeItem'>
  save: (shellId: string) => Promise<unknown>
}

/** Pasa la terminal guardada en el navegador a los ajustes, una sola vez, y borra la clave vieja. */
export async function migrateDefaultShell({ current, shells, storage, save }: Migration) {
  try {
    if (!current) {
      const id = shellToMigrate(storage.getItem(SHELL_KEY), shells)
      if (!id) return // todavía no se detectó ninguna: se reintenta la próxima vez
      await save(id)
    }
    storage.removeItem(SHELL_KEY)
  } catch (e) {
    if (!current) throw e // sin guardar no se borra nada
    // sin almacenamiento: no hay clave vieja que borrar
  }
}
