import { describe, expect, it } from 'vitest'
import { baseName, buildRows, favoriteConns, FAV_KEY, isWithin, parentOf } from './groups'
import type { Connection } from './api'

// Conexión mínima para armar el árbol; solo importan id y group.
const conn = (id: string, group = ''): Connection => ({
  id,
  name: id,
  group,
  host: 'h',
  port: 22,
  user: 'u',
  authType: 'password',
  hasSecret: false,
})

// Resume las filas como "g:A@0" (grupo, profundidad) y "c:id@1" para comparar fácil.
const shape = (rows: ReturnType<typeof buildRows>) => rows.map((r) => `${r.key}@${r.depth}`)

describe('baseName', () => {
  it('devuelve el último tramo de la ruta', () => {
    expect(baseName('A/B/C')).toBe('C')
    expect(baseName('A/B')).toBe('B')
  })
  it('un grupo sin barras es su propio nombre', () => {
    expect(baseName('A')).toBe('A')
  })
  it('la ruta vacía da vacío', () => {
    expect(baseName('')).toBe('')
  })
})

describe('parentOf', () => {
  it('devuelve la ruta del grupo que contiene', () => {
    expect(parentOf('A/B/C')).toBe('A/B')
    expect(parentOf('A/B')).toBe('A')
  })
  it('un grupo de primer nivel y la ruta vacía no tienen padre', () => {
    expect(parentOf('A')).toBe('')
    expect(parentOf('')).toBe('')
  })
})

describe('isWithin', () => {
  it('una ruta está dentro de sí misma', () => {
    expect(isWithin('A', 'A')).toBe(true)
    expect(isWithin('A/B', 'A/B')).toBe(true)
  })
  it('un descendiente está dentro de su ancestro', () => {
    expect(isWithin('A/B', 'A')).toBe(true)
    expect(isWithin('A/B/C', 'A')).toBe(true)
    expect(isWithin('A/B/C', 'A/B')).toBe(true)
  })
  it('un ancestro no está dentro de su descendiente', () => {
    expect(isWithin('A', 'A/B')).toBe(false)
  })
  it('no confunde prefijos de nombre con ancestros', () => {
    expect(isWithin('AB', 'A')).toBe(false)
    expect(isWithin('A/BC', 'A/B')).toBe(false)
  })
  it('rutas sin relación', () => {
    expect(isWithin('B', 'A')).toBe(false)
  })
})

describe('buildRows', () => {
  it('sin conexiones ni grupos no hay filas', () => {
    expect(buildRows([], [], {})).toEqual([])
  })

  it('las conexiones sin grupo quedan en la raíz, en el orden en que llegan', () => {
    const rows = buildRows([conn('b'), conn('a')], [], {})
    expect(shape(rows)).toEqual(['c:b@0', 'c:a@0'])
  })

  it('en cada nivel van primero los subgrupos y después los servidores', () => {
    const rows = buildRows([conn('suelta'), conn('web1', 'Prod')], [], {})
    expect(shape(rows)).toEqual(['g:Prod@0', 'c:web1@1', 'c:suelta@0'])
  })

  it('arma grupos anidados con su profundidad y la ruta completa', () => {
    const rows = buildRows([conn('x', 'A/B'), conn('y', 'A')], [], {})
    expect(shape(rows)).toEqual(['g:A@0', 'g:A/B@1', 'c:x@2', 'c:y@1'])
    const b = rows[1]
    expect(b).toMatchObject({ kind: 'group', path: 'A/B', name: 'B', depth: 1, open: true })
  })

  it('el contador de un grupo incluye lo de sus subgrupos', () => {
    const rows = buildRows([conn('x', 'A/B'), conn('y', 'A'), conn('z', 'A/B/C')], [], {})
    const counts = Object.fromEntries(rows.flatMap((r) => (r.kind === 'group' ? [[r.path, r.count]] : [])))
    expect(counts).toEqual({ A: 3, 'A/B': 2, 'A/B/C': 1 })
  })

  it('conserva los grupos vacíos que vienen en extra', () => {
    const rows = buildRows([], ['Vacío'], {})
    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({ kind: 'group', path: 'Vacío', count: 0, open: true })
  })

  it('un grupo vacío anidado crea también a sus ancestros', () => {
    const rows = buildRows([], ['X/Y'], {})
    expect(shape(rows)).toEqual(['g:X@0', 'g:X/Y@1'])
    expect(rows.map((r) => (r.kind === 'group' ? r.count : -1))).toEqual([0, 0])
  })

  it('un grupo de extra que ya tiene servidores no se duplica', () => {
    const rows = buildRows([conn('web1', 'Prod')], ['Prod'], {})
    expect(shape(rows)).toEqual(['g:Prod@0', 'c:web1@1'])
  })

  it('un grupo plegado oculta todo lo que tiene adentro pero sigue contando', () => {
    const rows = buildRows([conn('x', 'A/B'), conn('y', 'A'), conn('z')], [], { A: true })
    expect(shape(rows)).toEqual(['g:A@0', 'c:z@0'])
    expect(rows[0]).toMatchObject({ kind: 'group', path: 'A', open: false, count: 2 })
  })

  it('plegar un subgrupo no oculta a su padre ni a los hermanos', () => {
    const rows = buildRows([conn('x', 'A/B'), conn('y', 'A')], [], { 'A/B': true })
    expect(shape(rows)).toEqual(['g:A@0', 'g:A/B@1', 'c:y@1'])
    expect(rows[1]).toMatchObject({ path: 'A/B', open: false })
  })

  it('un grupo vacío dentro de uno plegado tampoco se ve', () => {
    const rows = buildRows([], ['A/B'], { A: true })
    expect(shape(rows)).toEqual(['g:A@0'])
  })

  it('plegados por defecto: sin estado guardado solo se ven los grupos de primer nivel', () => {
    const rows = buildRows([conn('x', 'A/B'), conn('y', 'A'), conn('z')], [], {}, true)
    expect(shape(rows)).toEqual(['g:A@0', 'c:z@0'])
    expect(rows[0]).toMatchObject({ path: 'A', open: false, count: 2 })
  })

  it('plegados por defecto: un grupo abierto a mano muestra sus subgrupos plegados', () => {
    const rows = buildRows([conn('x', 'A/B'), conn('y', 'A')], [], { A: false }, true)
    expect(shape(rows)).toEqual(['g:A@0', 'g:A/B@1', 'c:y@1'])
    expect(rows[1]).toMatchObject({ path: 'A/B', open: false })
  })

  it('respeta el orden del usuario: primero lo de extra y después lo de conns, sin ordenar alfabéticamente', () => {
    const rows = buildRows([conn('c1', 'B'), conn('c2', 'A')], ['Z'], {})
    expect(shape(rows)).toEqual(['g:Z@0', 'g:B@0', 'c:c1@1', 'g:A@0', 'c:c2@1'])
  })

  it('mantiene el orden de los servidores dentro de un grupo', () => {
    const rows = buildRows([conn('b', 'G'), conn('a', 'G'), conn('c', 'G')], [], {})
    expect(shape(rows)).toEqual(['g:G@0', 'c:b@1', 'c:a@1', 'c:c@1'])
  })

  it('las filas de conexión llevan la conexión original', () => {
    const c = conn('web1', 'Prod')
    const row = buildRows([c], [], {})[1]
    expect(row.kind).toBe('conn')
    if (row.kind === 'conn') expect(row.c).toBe(c)
  })
})

describe('favoriteConns', () => {
  const fav = (id: string, favOrder?: number): Connection => ({ ...conn(id), favorite: true, favOrder })

  it('deja solo las favoritas, por su orden y después por nombre', () => {
    const list = [conn('x'), fav('c', 2), fav('b', 1), fav('a'), fav('d', 2)]
    expect(favoriteConns(list).map((c) => c.id)).toEqual(['a', 'b', 'c', 'd'])
  })

  it('no modifica la lista de entrada ni mezcla favoritas de otro filtro', () => {
    const list = [fav('b', 2), fav('a', 1)]
    favoriteConns(list)
    expect(list.map((c) => c.id)).toEqual(['b', 'a'])
    expect(favoriteConns([])).toEqual([])
  })

  it('la clave de plegado no es una ruta de grupo válida', () => {
    expect(FAV_KEY.split('/').filter(Boolean).join('/')).not.toBe(FAV_KEY)
  })
})
