import { describe, expect, it } from 'vitest'
import type { Cred, Site } from './api'
import { credMatches, filterSites, placeBeforeId, siteForTab } from './sites'

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

describe('siteForTab', () => {
  it('el sitio que abrió la pestaña gana aunque la dirección haya cambiado', () => {
    expect(siteForTab(list, 'c', 'https://console.aws.amazon.com/home?region=us-east-1')?.id).toBe('c')
    expect(siteForTab(list, 'c', 'https://panel.hosting.com')?.id).toBe('c')
  })

  it('sin sitio de origen, o si ya no existe, busca por dirección', () => {
    expect(siteForTab(list, undefined, 'https://dns.example.org')?.id).toBe('b')
    expect(siteForTab(list, 'borrado', 'https://dns.example.org')?.id).toBe('b')
    expect(siteForTab(list, undefined, 'https://otro.example.com')).toBeUndefined()
  })
})

describe('credMatches', () => {
  const c = (domain?: string): Cred => ({ id: 'c', label: 'x', domain })
  it('el host de la página o un subdominio', () => {
    expect(credMatches(c('empresa.com'), 'https://empresa.com/login')).toBe(true)
    expect(credMatches(c('empresa.com'), 'https://Panel.Empresa.com:8443/x')).toBe(true)
    expect(credMatches(c('panel.empresa.com'), 'https://empresa.com/')).toBe(false)
    expect(credMatches(c('empresa.com'), 'https://otraempresa.com/')).toBe(false)
    expect(credMatches(c('empresa.com'), 'https://empresa.com.evil.io/')).toBe(false)
  })
  it('sin dominio, nunca', () => {
    expect(credMatches(c(), 'https://empresa.com/')).toBe(false)
    expect(credMatches(c(''), '')).toBe(false)
  })
})
