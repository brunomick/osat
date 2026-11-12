import { describe, expect, it } from 'vitest'
import { resolve, termThemes, type TermThemeName } from './termthemes'

const names = Object.keys(termThemes) as TermThemeName[]
const hex = /^#[0-9a-f]{6}([0-9a-f]{2})?$/i

describe('termthemes', () => {
  it('hay un tema por cada valor del ajuste', () => {
    expect(names.sort()).toEqual([
      'catppuccin-latte', 'catppuccin-mocha', 'dracula', 'everforest', 'github-dark', 'github-light', 'gruvbox-dark', 'kanagawa',
      'monokai', 'nord', 'one-dark', 'osat-dark', 'osat-light', 'rose-pine', 'rose-pine-dawn', 'solarized-dark', 'solarized-light', 'tokyo-night',
    ])
  })

  it.each(names)('%s tiene todos los campos con un color válido', (name) => {
    const t = termThemes[name]
    expect(Object.keys(t)).toHaveLength(22)
    for (const [k, v] of Object.entries(t)) expect(v, `${name}.${k}`).toMatch(hex)
  })

  it('osat oscuro es la paleta de siempre', () => {
    const t = termThemes['osat-dark']
    expect(t.background).toBe('#0a0a0a')
    expect(t.foreground).toBe('#d8d8d8')
    expect(t.red).toBe('#e06c75')
    expect(t.brightWhite).toBe('#fafafa')
  })

  it('osat claro: fondo blanco y texto en tinta', () => {
    expect(termThemes['osat-light'].background).toBe('#ffffff')
    expect(termThemes['osat-light'].foreground).toBe('#0a0a0a')
  })

  it.each(names)('resolve("%s") devuelve ese tema con cualquier tema de interfaz', (name) => {
    expect(resolve(name, 'dark')).toBe(termThemes[name])
    expect(resolve(name, 'light')).toBe(termThemes[name])
  })

  it('"auto" sigue el tema de la interfaz', () => {
    expect(resolve('auto', 'dark')).toBe(termThemes['osat-dark'])
    expect(resolve('auto', 'light')).toBe(termThemes['osat-light'])
    expect(resolve('auto', 'dracula')).toBe(termThemes['dracula'])
    expect(resolve('auto', 'solarized-light')).toBe(termThemes['solarized-light'])
  })

  it('un nombre desconocido cae en osat oscuro', () => {
    expect(resolve('nada' as never, 'light')).toBe(termThemes['osat-dark'])
  })
})
