import { writable } from 'svelte/store'
import { tr } from './i18n'

// Diálogos modales basados en promesas: `await askSecret(...)`.

export type DialogReq =
  | {
      type: 'secret'
      title: string
      message: string
      label: string
      allowSave: boolean
      okText?: string // "Conectar" si no se indica
      resolve: (v: { secret: string; save: boolean } | null) => void
    }
  | {
      type: 'confirm'
      title: string
      message: string
      details?: [string, string][]
      okText: string
      danger: boolean
      resolve: (ok: boolean) => void
    }
  | {
      type: 'prompt'
      title: string
      message: string
      value: string
      /** Selecciona al abrir solo hasta aquí (p. ej. el nombre sin la extensión). */
      selectEnd?: number
      okText: string
      resolve: (v: string | null) => void
    }
  | {
      type: 'choice'
      title: string
      message: string
      list?: string[]
      choices: { value: string; label: string; kind?: 'primary' | 'danger' }[]
      resolve: (v: string | null) => void
    }
  | {
      type: 'questions'
      title: string
      message: string
      questions: { text: string; echo: boolean; value?: string }[]
      resolve: (v: string[] | null) => void
    }

export const dialog = writable<DialogReq | null>(null)

// Cola: si dos pestañas piden un diálogo a la vez, el segundo espera su turno
// en vez de pisar al primero (cuya promesa nunca se resolvería).
const queue: DialogReq[] = []

function show<T>(build: (resolve: (v: T) => void) => DialogReq): Promise<T> {
  return new Promise<T>((res) => {
    queue.push(
      build((v: T) => {
        queue.shift()
        dialog.set(queue[0] ?? null)
        res(v)
      }),
    )
    if (queue.length === 1) dialog.set(queue[0])
  })
}

export const choiceDialog = (o: {
  title: string
  message: string
  list?: string[]
  choices: { value: string; label: string; kind?: 'primary' | 'danger' }[]
}) => show<string | null>((resolve) => ({ type: 'choice', ...o, resolve }))

export const questionsDialog = (o: { title: string; message: string; questions: { text: string; echo: boolean; value?: string }[] }) =>
  show<string[] | null>((resolve) => ({ type: 'questions', ...o, resolve }))

export const askSecret = (o: { title: string; message: string; label: string; allowSave?: boolean; okText?: string }) =>
  show<{ secret: string; save: boolean } | null>((resolve) => ({
    type: 'secret',
    allowSave: o.allowSave ?? true,
    ...o,
    resolve,
  }))

export const confirmDialog = (o: {
  title: string
  message: string
  details?: [string, string][]
  okText?: string
  danger?: boolean
}) =>
  show<boolean>((resolve) => ({
    type: 'confirm',
    okText: o.okText ?? tr('common.accept'),
    danger: o.danger ?? false,
    title: o.title,
    message: o.message,
    details: o.details,
    resolve,
  }))

export const promptDialog = (o: { title: string; message: string; value?: string; selectEnd?: number; okText?: string }) =>
  show<string | null>((resolve) => ({
    type: 'prompt',
    value: o.value ?? '',
    selectEnd: o.selectEnd,
    okText: o.okText ?? tr('common.accept'),
    title: o.title,
    message: o.message,
    resolve,
  }))

/** Extensión de un archivo (".txt"), o "" si no tiene ("Makefile", ".bashrc"). */
function extOf(name: string) {
  const i = name.lastIndexOf('.')
  return i > 0 ? name.slice(i) : ''
}

/**
 * Pide el nombre nuevo de un archivo o carpeta. Como el Explorador: en los
 * archivos queda seleccionado solo el nombre (la extensión no se pisa al
 * escribir) y si igual se cambia la extensión, pide confirmarlo.
 * Devuelve null si se canceló o no cambió.
 */
export async function renameDialog(e: { name: string; path: string; isDir: boolean }) {
  const ext = e.isDir ? '' : extOf(e.name)
  for (let value = e.name; ; ) {
    const name = await promptDialog({
      title: tr('dialog.rename'),
      message: e.path,
      value,
      selectEnd: value.length - (ext && value.endsWith(ext) ? ext.length : 0),
      okText: tr('dialog.rename'),
    })
    if (!name || name === e.name) return null
    const next = e.isDir ? '' : extOf(name)
    if (next.toLowerCase() === ext.toLowerCase()) return name
    const ok = await confirmDialog({
      title: tr('dialog.extTitle'),
      message: ext
        ? next
          ? tr('dialog.extChange', { ext, next })
          : tr('dialog.extRemove', { ext })
        : tr('dialog.extAdd', { next }),
      okText: tr('dialog.extOk'),
    })
    if (ok) return name
    value = name // volver a editar lo que se había escrito
  }
}
