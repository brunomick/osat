// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import source from './sitefill.js?raw'

// sitefill.js no lo usa el frontend: Go lo embebe y lo corre en la página del sitio (BrowserFill).
// Es una expresión que da una función: se evalúa igual que en la página.
type Fill = (d: { domain?: string; account?: string; user?: string; password?: string; probe?: boolean; auto?: boolean }) => string
const fill = (0, eval)(source) as Fill

const cred = { domain: 'localhost', account: '123456789012', user: 'admin', password: 'hunter2' }
const val = (id: string) => (document.getElementById(id) as HTMLInputElement).value

beforeEach(() => {
  // jsdom no hace layout: todo lo que no esté marcado como oculto cuenta como visible.
  vi.spyOn(Element.prototype, 'getClientRects').mockImplementation(function (this: Element) {
    return (this.hasAttribute('data-hidden') ? [] : [{}]) as unknown as DOMRectList
  })
})

describe('sitefill', () => {
  it('rellena usuario y contraseña de un login común', () => {
    document.body.innerHTML = `
      <input id="q" type="search">
      <form><input id="u" name="email"><input id="p" type="password"><button>Entrar</button></form>`
    expect(fill(cred)).toBe('ok')
    expect([val('q'), val('u'), val('p')]).toEqual(['', 'admin', 'hunter2'])
  })

  it('avisa a React: dispara input y change', () => {
    document.body.innerHTML = `<input id="u" autocomplete="username"><input id="p" type="password">`
    const seen: string[] = []
    for (const id of ['u', 'p']) {
      const el = document.getElementById(id)!
      el.addEventListener('input', () => seen.push(id + ':input'))
      el.addEventListener('change', () => seen.push(id + ':change'))
    }
    fill(cred)
    expect(seen).toEqual(['u:input', 'u:change', 'p:input', 'p:change'])
  })

  it('el campo cuenta va aparte del usuario', () => {
    document.body.innerHTML = `
      <form><input id="a" name="account"><input id="u" name="username"><input id="p" type="password"></form>`
    expect(fill(cred)).toBe('ok')
    expect([val('a'), val('u'), val('p')]).toEqual(['123456789012', 'admin', 'hunter2'])
  })

  it('sin pistas, el usuario es el texto anterior a la contraseña', () => {
    document.body.innerHTML = `<form><input id="x"><input id="y"><input id="p" type="password"></form>`
    fill(cred)
    expect([val('x'), val('y'), val('p')]).toEqual(['', 'admin', 'hunter2'])
  })

  it('login en dos pasos: la página del usuario sola', () => {
    document.body.innerHTML = `<input id="s" type="search"><input id="u" type="email" name="loginfmt">`
    expect(fill(cred)).toBe('ok')
    expect([val('s'), val('u')]).toEqual(['', 'admin'])
  })

  it('sin contraseña ni pistas no toca nada (un buscador no es un login)', () => {
    document.body.innerHTML = `<input id="q" name="q">`
    expect(fill(cred)).toBe('form')
    expect(val('q')).toBe('')
  })

  it('saltea campos ocultos, deshabilitados y de solo lectura', () => {
    document.body.innerHTML = `
      <input id="h" type="password" data-hidden><input id="d" type="password" disabled>
      <input id="u" name="user"><input id="p" type="password">`
    fill(cred)
    expect([val('h'), val('d'), val('u'), val('p')]).toEqual(['', '', 'admin', 'hunter2'])
  })

  it('no rellena en otro dominio', () => {
    document.body.innerHTML = `<input id="u" name="user"><input id="p" type="password">`
    expect(fill({ ...cred, domain: 'empresa.com' })).toBe('host')
    expect(fill({ ...cred, domain: 'calhost' })).toBe('host') // "localhost" termina igual pero no es subdominio
    expect(fill({ ...cred, domain: '' })).toBe('host')
    expect([val('u'), val('p')]).toEqual(['', ''])
  })

  it('los datos vacíos no pisan lo que ya está escrito', () => {
    document.body.innerHTML = `<input id="u" name="user" value="ya"><input id="p" type="password">`
    expect(fill({ ...cred, user: '' })).toBe('ok')
    expect([val('u'), val('p')]).toEqual(['ya', 'hunter2'])
  })
})

describe('sitefill, modo probe (¿hay un login?)', () => {
  it('con contraseña o usuario con pistas, sí; un buscador, no', () => {
    document.body.innerHTML = `<input name="user"><input id="p" type="password">`
    expect(fill({ probe: true })).toBe('login')
    document.body.innerHTML = `<input type="email" name="loginfmt">`
    expect(fill({ probe: true })).toBe('login')
    document.body.innerHTML = `<input name="account">`
    expect(fill({ probe: true })).toBe('login')
    document.body.innerHTML = `<input name="q">`
    expect(fill({ probe: true })).toBe('none')
  })
  it('no escribe nada', () => {
    document.body.innerHTML = `<input id="u" name="user"><input id="p" type="password">`
    fill({ probe: true, user: 'x', password: 'y' })
    expect([val('u'), val('p')]).toEqual(['', ''])
  })
})

describe('sitefill, modo auto (relleno sin preguntar)', () => {
  it('no pisa lo que la persona ya escribió', () => {
    document.body.innerHTML = `<input id="u" name="user" value="otro"><input id="p" type="password">`
    expect(fill({ ...cred, auto: true })).toBe('ok')
    expect([val('u'), val('p')]).toEqual(['otro', 'hunter2'])
  })
  it('si ya está todo escrito, no hay nada que rellenar', () => {
    document.body.innerHTML = `<input id="u" name="user" value="a"><input id="p" type="password" value="b">`
    expect(fill({ ...cred, auto: true })).toBe('form')
    expect([val('u'), val('p')]).toEqual(['a', 'b'])
  })
})
