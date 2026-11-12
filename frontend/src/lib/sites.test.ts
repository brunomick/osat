import { describe, expect, it } from 'vitest'
import type { Site } from './api'
import { filterSites, placeBeforeId } from './sites'

const s = (id: string, name: string, url: string, order: number): Site => ({ id, name, url, order })
const list = [s('c', 'Consola AWS', 'https://console.aws.amazon.com', 3), s('a', 'Hosting', 'https://panel.hosting.com', 1), s('b', 'DNS', 'https://dns.example.org', 2)]

describe('filterSites', () => {
  it('sin búsqueda devuelve todos, por orden', () => {
    expect(filterSites(list, '').map((x) => x.id)).toEqual(['a', 'b', 'c'])
    expect(filterSites(list, '   ').map((x) => x.id)).toEqual(['a', 'b', 'c'])
  })

  it('busca en el nombre sin distinguir mayúsculas', () => {
    expect(filterSites(list, 'consola').map((x) => x.id)).toEqual(['c'])
  })

  it('busca en la dirección', () => {
    expect(filterSites(list, 'example.org').map((x) => x.id)).toEqual(['b'])
  })

  it('sin coincidencias devuelve vacío y no modifica la lista original', () => {
    expect(filterSites(list, 'zzz')).toEqual([])
    expect(list.map((x) => x.id)).toEqual(['c', 'a', 'b'])
  })
})

describe('placeBeforeId', () => {
  const shown = filterSites(list, '') // a, b, c
  it('sin fila de referencia va al final', () => {
    expect(placeBeforeId(shown, 'a', undefined, undefined)).toBe('')
  })

  it('arriba de una fila: esa fila', () => {
    expect(placeBeforeId(shown, 'c', 'b', 'before')).toBe('b')
  })

  it('abajo de una fila: la que le sigue', () => {
    expect(placeBeforeId(shown, 'c', 'a', 'after')).toBe('b')
  })

  it('abajo de una fila salteando la que se arrastra', () => {
    expect(placeBeforeId(shown, 'b', 'a', 'after')).toBe('c')
  })

  it('abajo de la última va al final', () => {
    expect(placeBeforeId(shown, 'a', 'c', 'after')).toBe('')
  })
})
