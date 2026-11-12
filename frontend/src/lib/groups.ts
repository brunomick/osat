import type { Connection } from './api'

// Los grupos son carpetas anidadas: el grupo de una conexión es una ruta
// "Producción/Web" ("" = sin grupo). Mismo formato que el backend (store.NormalizeGroup).

export type Row =
  | { kind: 'group'; key: string; path: string; name: string; depth: number; count: number; open: boolean }
  | { kind: 'conn'; key: string; c: Connection; depth: number }

interface Node {
  groups: Map<string, Node>
  conns: Connection[]
}

const newNode = (): Node => ({ groups: new Map(), conns: [] })
const countOf = (n: Node): number => n.conns.length + [...n.groups.values()].reduce((s, g) => s + countOf(g), 0)

/** Último tramo de una ruta ("A/B" → "B"). */
export const baseName = (path: string) => path.slice(path.lastIndexOf('/') + 1)

/** Ruta del grupo que contiene a `path` ("A/B" → "A"; "A" → ""). */
export const parentOf = (path: string) => (path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '')

/** True si `path` es `ancestor` o está adentro de él. */
export const isWithin = (path: string, ancestor: string) => path === ancestor || path.startsWith(ancestor + '/')

/**
 * Árbol de grupos y servidores en filas planas, en el orden en que se dibujan:
 * primero los subgrupos de cada nivel y después sus servidores. `extra` son
 * grupos que pueden estar vacíos; los plegados en `collapsed` ocultan todo lo
 * que tienen adentro (un grupo que no figura usa `defaultCollapsed`). Se
 * respeta el orden en que llegan (el que eligió el usuario): los grupos por su
 * primera aparición en `extra` y después en `conns`.
 */
export function buildRows(conns: Connection[], extra: string[], collapsed: Record<string, boolean>, defaultCollapsed = false): Row[] {
  const root = newNode()
  const nodeAt = (path: string) => {
    let n = root
    for (const part of path.split('/').filter(Boolean)) {
      let next = n.groups.get(part)
      if (!next) n.groups.set(part, (next = newNode()))
      n = next
    }
    return n
  }
  for (const g of extra) nodeAt(g)
  for (const c of conns) nodeAt(c.group).conns.push(c)

  const rows: Row[] = []
  const walk = (n: Node, prefix: string, depth: number) => {
    for (const name of n.groups.keys()) {
      const child = n.groups.get(name)!
      const path = prefix ? `${prefix}/${name}` : name
      const open = !(collapsed[path] ?? defaultCollapsed)
      rows.push({ kind: 'group', key: `g:${path}`, path, name, depth, count: countOf(child), open })
      if (open) walk(child, path, depth + 1)
    }
    for (const c of n.conns) rows.push({ kind: 'conn', key: `c:${c.id}`, c, depth })
  }
  walk(root, '', 0)
  return rows
}

/** Clave reservada de la sección Favoritos en el mapa de plegados (no choca con una ruta de grupo: no puede tener tramos vacíos). */
export const FAV_KEY = '//favoritos'

/** Las conexiones favoritas en su orden de Favoritos (FavOrder y, a igual lugar, por nombre). */
export function favoriteConns(conns: Connection[]): Connection[] {
  return conns
    .filter((c) => c.favorite)
    .sort((a, b) => (a.favOrder ?? 0) - (b.favOrder ?? 0) || a.name.localeCompare(b.name))
}
