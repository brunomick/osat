import { afterEach, describe, expect, it, vi } from 'vitest'
import { get } from 'svelte/store'
import { dictionaries, locale, resolveLanguage, t, tr } from './i18n'

const vars = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort()

afterEach(() => locale.set('es'))

describe('diccionarios', () => {
  it('tienen las mismas claves', () => {
    expect(Object.keys(dictionaries.en).sort()).toEqual(Object.keys(dictionaries.es).sort())
  })
  it('usan las mismas variables en cada clave', () => {
    for (const k of Object.keys(dictionaries.es)) expect(vars(dictionaries.en[k]), k).toEqual(vars(dictionaries.es[k]))
  })
})

describe('t / tr', () => {
  it('arranca en español', () => {
    expect(tr('settings.language')).toBe('Idioma')
  })
  it('interpola variables', () => {
    expect(tr('example.connectTo', { name: 'srv' })).toBe('Conectar a srv')
    expect(get(t)('example.items.other', { count: 3 })).toBe('3 elementos')
  })
  it('cambia en caliente', () => {
    const f = get(t)
    locale.set('en')
    expect(tr('settings.language')).toBe('Language')
    expect(get(t)('settings.language')).toBe('Language')
    expect(f('settings.language')).toBe('Idioma') // la función anterior no cambia; el store sí
  })
  it('una clave inexistente devuelve la clave y avisa', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(tr('no.existe')).toBe('no.existe')
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
  })
})

describe('resolveLanguage', () => {
  it('respeta es/en y resuelve system con el idioma del sistema', () => {
    expect(resolveLanguage('en', 'es-AR')).toBe('en')
    expect(resolveLanguage('es', 'en-US')).toBe('es')
    expect(resolveLanguage('system', 'es-AR')).toBe('es')
    expect(resolveLanguage('system', 'fr-FR')).toBe('en')
  })
})

// El código no puede usar una clave que no existe: se buscan las llamadas tr('clave'), $t('clave') y similares
// con la clave escrita entera (las que se arman con variables se prueban en sus propios tests).
describe('claves usadas en el código', () => {
  const sources = import.meta.glob(['../**/*.svelte', './*.ts', '!./*.test.ts', '!./i18n.ts'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>
  it('existen en los dos diccionarios', () => {
    const missing: string[] = []
    for (const [file, code] of Object.entries(sources)) {
      for (const m of code.matchAll(/(?:\$t|\$tl|\btr|\btf|\bt)\(\s*['"]([a-z][\w.]*)['"]/g)) {
        if (!(m[1] in dictionaries.es) || !(m[1] in dictionaries.en)) missing.push(`${file}: ${m[1]}`)
      }
    }
    expect(missing).toEqual([])
  })
})
