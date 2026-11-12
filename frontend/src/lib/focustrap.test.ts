// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { focusTrap } from './focustrap'

let opener: HTMLButtonElement
let dlg: HTMLDivElement

const tab = (el: Element, shiftKey = false) => el.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey, bubbles: true, cancelable: true }))

beforeEach(() => {
  document.body.innerHTML = '<button id="opener">abrir</button><div id="dlg"><button id="a">A</button><input id="b" /><button id="c">C</button><button id="d" disabled>D</button></div>'
  opener = document.getElementById('opener') as HTMLButtonElement
  dlg = document.getElementById('dlg') as HTMLDivElement
  opener.focus()
})
afterEach(() => (document.body.innerHTML = ''))

describe('focusTrap', () => {
  it('enfoca el primer control al abrirse', () => {
    focusTrap(dlg)
    expect(document.activeElement?.id).toBe('a')
  })

  it('enfoca el control que se indica', () => {
    focusTrap(dlg, '#b')
    expect(document.activeElement?.id).toBe('b')
  })

  it('respeta el foco si el diálogo ya enfocó algo adentro', () => {
    document.getElementById('c')!.focus()
    focusTrap(dlg)
    expect(document.activeElement?.id).toBe('c')
  })

  it('Tab en el último control vuelve al primero (salta los deshabilitados)', () => {
    focusTrap(dlg)
    document.getElementById('c')!.focus()
    tab(document.activeElement!)
    expect(document.activeElement?.id).toBe('a')
  })

  it('Shift+Tab en el primero va al último', () => {
    focusTrap(dlg)
    tab(document.activeElement!, true)
    expect(document.activeElement?.id).toBe('c')
  })

  it('Tab en el medio no se toca (lo hace el navegador)', () => {
    focusTrap(dlg, '#b')
    const e = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true })
    document.getElementById('b')!.dispatchEvent(e)
    expect(e.defaultPrevented).toBe(false)
  })

  it('al cerrarse devuelve el foco a quien lo tenía', () => {
    const t = focusTrap(dlg)
    expect(document.activeElement).not.toBe(opener)
    t.destroy()
    expect(document.activeElement).toBe(opener)
  })

  it('sin controles, el foco queda en el diálogo', () => {
    dlg.innerHTML = '<p>hola</p>'
    focusTrap(dlg)
    expect(document.activeElement).toBe(dlg)
  })
})
