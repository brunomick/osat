import { describe, expect, it } from 'vitest'
import { normalizeSiteUrl, sameSiteUrl, siteHost } from './siteUrl'

describe('normalizeSiteUrl', () => {
  it('agrega https:// si falta', () => {
    expect(normalizeSiteUrl('example.com')).toBe('https://example.com')
    expect(normalizeSiteUrl('  example.com/panel  ')).toBe('https://example.com/panel')
    expect(normalizeSiteUrl('localhost:3000')).toBe('https://localhost:3000')
  })

  it('deja igual una dirección http(s) completa', () => {
    expect(normalizeSiteUrl('http://x.y/a?b')).toBe('http://x.y/a?b')
    expect(normalizeSiteUrl('https://x.y')).toBe('https://x.y')
  })

  it('rechaza lo que no es http(s)', () => {
    expect(normalizeSiteUrl('javascript:alert(1)')).toBeNull()
    expect(normalizeSiteUrl('ftp://x')).toBeNull()
    expect(normalizeSiteUrl('data:text/html,<b>x</b>')).toBeNull()
    expect(normalizeSiteUrl('mailto:a@b.c')).toBeNull()
    expect(normalizeSiteUrl('file:///etc/passwd')).toBeNull()
  })

  it('rechaza lo vacío o sin host', () => {
    expect(normalizeSiteUrl('')).toBeNull()
    expect(normalizeSiteUrl('   ')).toBeNull()
    expect(normalizeSiteUrl('https://')).toBeNull()
    expect(normalizeSiteUrl('hola mundo')).toBeNull()
  })
})

describe('sameSiteUrl', () => {
  it('ignora la barra final del host', () => {
    expect(sameSiteUrl('https://x.com', 'https://x.com/')).toBe(true)
  })
  it('distingue páginas distintas', () => {
    expect(sameSiteUrl('https://x.com/a', 'https://x.com/b')).toBe(false)
  })
})

describe('siteHost', () => {
  it('devuelve el host, o la dirección si no se entiende', () => {
    expect(siteHost('https://panel.example.com:8443/a')).toBe('panel.example.com')
    expect(siteHost('no es una dirección')).toBe('no es una dirección')
  })
})
