import { get } from 'svelte/store'
import { codedText, errorText, parseError, type ConflictPolicy } from './api'
import { choiceDialog } from './dialog'
import { tr } from './i18n'
import { settings, toast } from './stores'

/** Arranca una transferencia con la política de los ajustes ante lo que ya existe en el destino; con "preguntar", si algo existe, pregunta qué hacer. */
export async function transfer(run: (policy: ConflictPolicy) => Promise<unknown>) {
  try {
    try {
      await run(get(settings).files.onConflict)
    } catch (e) {
      const ke = parseError(e)
      if (ke?.code !== 'CONFLICT') throw e
      const n = Number(ke.data?.count ?? 0)
      const policy = await choiceDialog({
        title: tr('transfer.existsTitle'),
        message:
          n === 1
            ? tr('transfer.existsOne')
            : tr('transfer.existsMany', { count: n }),
        list: (ke.data?.names ?? '').split('\n'),
        choices: [
          { value: 'skip', label: tr('transfer.skip') },
          { value: 'overwrite', label: tr('transfer.replace'), kind: 'primary' },
        ],
      })
      if (policy) await run(policy as ConflictPolicy)
    }
  } catch (e) {
    toast(errorText(e), 'error')
  }
}

/** Mensaje para un error de mover (CONFLICT lista los nombres que chocan). */
export function moveErrorText(e: unknown) {
  const ke = parseError(e)
  return ke?.code === 'CONFLICT' ? `${codedText(ke)} (${(ke.data?.names ?? '').split('\n').join(', ')})` : errorText(e)
}
