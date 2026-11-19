// Portapapeles de archivos de OSAT ("Copiar" / "Pegar" en los exploradores).
// Es propio de la app: guarda de qué explorador salieron las rutas, para que
// "Pegar" decida qué hacer (copiar en el mismo servidor / PC, subir, descargar).
//
// "Pegar" también toma lo que hay en el portapapeles del sistema (lo copiado en
// el Explorador o en un escritorio remoto), si se copió después que lo de OSAT.
import { get, writable } from 'svelte/store'
import { api, type ClipboardFilesInfo } from './api'
import type { FsSource } from './filedrag'

export interface FileClip {
  source: FsSource
  paths: string[]
  /** Número del portapapeles del sistema al copiar: si después cambió, se copió algo afuera. */
  seq?: number
}

export const fileClip = writable<FileClip | null>(null)

/** Archivos en el portapapeles del sistema (null si no se pudo saber). */
export const systemFiles = writable<ClipboardFilesInfo | null>(null)

export async function refreshSystemFiles(): Promise<ClipboardFilesInfo | null> {
  try {
    systemFiles.set(await api.clipboardFiles())
  } catch {
    systemFiles.set(null) // sin backend, o fuera de Windows
  }
  return get(systemFiles)
}

/** Copiar en un explorador de OSAT: recuerda el portapapeles del sistema de ese momento. */
export async function copyFiles(source: FsSource, paths: string[]) {
  const sys = await refreshSystemFiles()
  fileClip.set({ source, paths, seq: sys?.seq })
}

/** "Pegar" toma lo del sistema: hay archivos ahí y se copiaron después que lo de OSAT. */
export function pasteFromSystem(): boolean {
  const sys = get(systemFiles)
  if (!sys?.kind) return false
  const clip = get(fileClip)
  return !clip || clip.seq !== sys.seq
}

/** Hay algo para pegar (de OSAT o del sistema). */
export const canPaste = () => !!get(fileClip) || !!get(systemFiles)?.kind

// Al volver a osat puede haber archivos nuevos copiados afuera.
if (typeof window !== 'undefined') window.addEventListener('focus', () => void refreshSystemFiles())
