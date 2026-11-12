import { describe, expect, it } from 'vitest'
// @ts-expect-error el proyecto no tiene @types/node; Vitest corre en Node y sí lo tiene
import { readFileSync } from 'node:fs'
import { appThemes } from './termthemes'

// Contraste WCAG AA de los pares de colores que la interfaz usa de verdad, en los dos temas (lee app.css).

const css: string = readFileSync(new URL('../app.css', import.meta.url), 'utf8')

function vars(selector: string): Record<string, string> {
  const start = css.indexOf(selector + ' {')
  const body = css.slice(css.indexOf('{', start) + 1, css.indexOf('\n}', start))
  const out: Record<string, string> = {}
  for (const m of body.matchAll(/(--[\w-]+):\s*([^;]+);/g)) out[m[1]] = m[2].trim()
  return out
}

const dark = vars(':root')
// Todos los temas de la app (Ajustes → Apariencia); cada uno tiene que tener su bloque en app.css.
const themes: Record<string, Record<string, string>> = { oscuro: dark }
for (const name of appThemes.filter((n) => n !== 'dark')) {
  const selector = `:root[data-theme='${name}']`
  if (!css.includes(selector + ' {')) throw new Error(`app.css no tiene el tema ${name}`)
  themes[name] = { ...dark, ...vars(selector) }
}

function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}
export function ratio(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

describe('ratio', () => {
  it('negro sobre blanco es 21:1', () => expect(ratio('#000000', '#ffffff')).toBeCloseTo(21, 5))
})

describe.each(Object.entries(themes))('contraste, tema %s', (_, t) => {
  const pairs: [string, string, number][] = []
  for (const fg of ['--text', '--text-2', '--text-3']) for (const bg of ['--bg', '--bg-2', '--bg-3', '--rail']) pairs.push([fg, bg, 4.5])
  for (const fg of ['--danger', '--warning', '--signal']) for (const bg of ['--bg', '--bg-2']) pairs.push([fg, bg, 4.5])
  pairs.push(['--on-accent', '--accent', 4.5])
  pairs.push(['--on-danger', '--danger', 4.5]) // ítems peligrosos del menú contextual
  // El foco de los campos (app.css) es un contorno de --text-2.
  for (const bg of ['--bg', '--bg-2']) pairs.push(['--text-2', bg, 3])

  it.each(pairs)('%s sobre %s ≥ %s:1', (fg, bg, min) => {
    expect(t[fg], fg).toMatch(/^#[0-9a-f]{6}$/i)
    expect(t[bg], bg).toMatch(/^#[0-9a-f]{6}$/i)
    expect(ratio(t[fg], t[bg])).toBeGreaterThanOrEqual(min)
  })
})
