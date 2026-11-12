import type { GitFile, GitStatus } from './api'

// Funciones puras del control de versiones de la pestaña Repositorio (RepoView, GitPanel):
// qué archivos van en "Preparados" y en "Cambios", con qué letra, y cómo se marcan en el árbol.

/** Un archivo en una de las dos listas del panel. */
export interface GitItem {
  file: GitFile
  /** En la lista de preparados (si no, en la de cambios). */
  staged: boolean
  /** Letra que se muestra: M, A, D, R, C, U. */
  letter: string
}

/** Las dos listas del panel, como en VS Code: lo preparado (x) y lo que cambió en la copia de trabajo (y). */
export function gitLists(files: GitFile[]): { staged: GitItem[]; changes: GitItem[] } {
  const staged: GitItem[] = []
  const changes: GitItem[] = []
  for (const f of files) {
    if (f.x === 'U' || f.y === 'U' || f.x === '?') {
      // En conflicto, o sin seguimiento ("Untracked", también con U, como en VS Code).
      changes.push({ file: f, staged: false, letter: 'U' })
      continue
    }
    if (f.x !== '.') staged.push({ file: f, staged: true, letter: f.x })
    if (f.y !== '.') changes.push({ file: f, staged: false, letter: f.y })
  }
  const byPath = (a: GitItem, b: GitItem) => a.file.path.localeCompare(b.file.path)
  return { staged: staged.sort(byPath), changes: changes.sort(byPath) }
}

export type MarkClass = 'added' | 'deleted' | 'modified' | 'conflict'

/** Color de una letra: nuevo o agregado verde, borrado rojo, en conflicto aparte, el resto (modificado...) amarillo. */
export function letterClass(item: GitItem): MarkClass {
  if (item.file.x === 'U' || item.file.y === 'U') return 'conflict'
  if (item.file.x === '?' || item.letter === 'A') return 'added'
  if (item.letter === 'D') return 'deleted'
  return 'modified'
}

export function joinPath(root: string, rel: string): string {
  return root === '/' ? '/' + rel : `${root.replace(/\/+$/, '')}/${rel}`
}

/** Marca de un archivo o carpeta del árbol. */
export interface TreeMark {
  letter: string // "" en carpetas (solo el color)
  cls: MarkClass
}

const rank: Record<MarkClass, number> = { conflict: 3, modified: 2, added: 1, deleted: 0 }

/** Ruta absoluta → marca, para pintar el árbol. Las carpetas toman el color más importante de lo que tienen adentro. */
export function treeMarks(st: GitStatus | null): Map<string, TreeMark> {
  const out = new Map<string, TreeMark>()
  if (!st?.isRepo) return out
  const { staged, changes } = gitLists(st.files)
  const top = st.root.replace(/\/+$/, '') || '/'
  // Lo de la copia de trabajo va después y manda sobre lo preparado (es lo que se ve en el editor).
  for (const it of [...staged, ...changes]) {
    const cls = letterClass(it)
    const abs = joinPath(st.root, it.file.path)
    out.set(abs, { letter: it.letter, cls })
    let dir = abs.slice(0, abs.lastIndexOf('/')) || '/'
    for (;;) {
      const prev = out.get(dir)
      if (!prev || rank[cls] > rank[prev.cls]) out.set(dir, { letter: '', cls })
      if (dir === top || dir === '/' || !dir.startsWith(top)) break
      dir = dir.slice(0, dir.lastIndexOf('/')) || '/'
    }
  }
  return out
}

/** Cantidad de archivos con cambios (el número sobre el ícono del panel). */
export const changeCount = (st: GitStatus | null) => (st?.isRepo ? st.files.length : 0)

/** Nombre del servicio del remoto, para mostrar. */
export function hostLabel(host: GitStatus['host']): string {
  return { github: 'GitHub', gitlab: 'GitLab', bitbucket: 'Bitbucket', other: 'git', '': '' }[host] ?? ''
}

/** "acme/infra" de una URL de remoto (https://github.com/acme/infra.git, git@host:acme/infra.git); si no se puede, la URL. */
export function repoName(url: string): string {
  const m = url.replace(/\.git$/, '').match(/[:/]([^/:]+\/[^/:]+)$/)
  return m ? m[1] : url
}

/** Rutas para descartar, separadas: las que tienen seguimiento vuelven atrás y las nuevas se borran. */
export function discardSets(items: GitItem[]): { tracked: string[]; untracked: string[] } {
  const tracked: string[] = []
  const untracked: string[] = []
  for (const it of items) (it.file.x === '?' ? untracked : tracked).push(it.file.path)
  return { tracked, untracked }
}
