// Portapapeles de archivos de OSAT ("Copiar" / "Pegar" en los exploradores).
// Es propio de la app: guarda de qué explorador salieron las rutas, para que
// "Pegar" decida qué hacer (copiar en el mismo servidor / PC, subir, descargar).
import { writable } from 'svelte/store'
import type { FsSource } from './filedrag'

export interface FileClip {
  source: FsSource
  paths: string[]
}

export const fileClip = writable<FileClip | null>(null)
