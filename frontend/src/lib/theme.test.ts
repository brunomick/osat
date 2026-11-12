// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { resolveAppearance } from './theme'

describe('resolveAppearance', () => {
  it('oscuro y claro no dependen del sistema', () => {
    expect(resolveAppearance('dark', true)).toBe('dark')
    expect(resolveAppearance('light', false)).toBe('light')
  })
  it('"sistema" sigue al sistema', () => {
    expect(resolveAppearance('system', true)).toBe('light')
    expect(resolveAppearance('system', false)).toBe('dark')
  })
  it('los temas de color pintan la app tal cual, sin mirar el sistema', () => {
    expect(resolveAppearance('dracula', true)).toBe('dracula')
    expect(resolveAppearance('solarized-light', false)).toBe('solarized-light')
  })
  it('un valor desconocido es oscuro', () => {
    expect(resolveAppearance('violeta', true)).toBe('dark')
  })
})
