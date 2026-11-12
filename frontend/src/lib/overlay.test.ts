import { get } from 'svelte/store'
import { describe, expect, it } from 'vitest'
import { holdOverlay, overlayHolder, overlayOpen } from './overlay'

describe('holdOverlay', () => {
  it('sin capas está cerrado', () => {
    expect(get(overlayOpen)).toBe(false)
  })

  it('dos holds y un release siguen abiertos; con el segundo release se cierra', () => {
    const a = holdOverlay()
    const b = holdOverlay()
    expect(get(overlayOpen)).toBe(true)
    a()
    expect(get(overlayOpen)).toBe(true)
    b()
    expect(get(overlayOpen)).toBe(false)
  })

  it('liberar dos veces la misma no baja de 0 ni suelta la de otro', () => {
    const a = holdOverlay()
    const b = holdOverlay()
    a()
    a()
    expect(get(overlayOpen)).toBe(true) // b sigue
    b()
    expect(get(overlayOpen)).toBe(false)
    const c = holdOverlay()
    expect(get(overlayOpen)).toBe(true) // el contador no quedó en negativo
    c()
    expect(get(overlayOpen)).toBe(false)
  })
})

describe('overlayHolder', () => {
  it('toma la marca una sola vez mientras la condición vale y la suelta al apagarse', () => {
    const hold = overlayHolder()
    hold(true)
    hold(true)
    expect(get(overlayOpen)).toBe(true)
    hold(false)
    expect(get(overlayOpen)).toBe(false)
    hold(false)
    expect(get(overlayOpen)).toBe(false)
  })
})
