// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { treeNav } from './treenav'

let tree: HTMLElement
const item = (id: string) => document.getElementById(id) as HTMLElement
const key = (el: HTMLElement, k: string, init: KeyboardEventInit = {}) => {
  const e = new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...init })
  el.dispatchEvent(e)
  return e
}

beforeEach(() => {
  document.body.innerHTML = `<div id="tree" role="tree">
    <button id="g" role="treeitem" aria-level="1" aria-expanded="true" tabindex="-1">Grupo</button>
    <div id="c1" role="treeitem" aria-level="2" tabindex="-1">uno<button id="x" tabindex="-1">x</button></div>
    <div id="c2" role="treeitem" aria-level="2" tabindex="-1">dos</div>
  </div>`
  tree = document.getElementById('tree')!
  treeNav(tree)
})

describe('treeNav', () => {
  it('deja una sola fila en el orden de Tab', () => {
    expect([...tree.querySelectorAll('[role=treeitem]')].map((e) => e.getAttribute('tabindex'))).toEqual(['0', '-1', '-1'])
  })

  it('las flechas mueven el foco y el tabindex 0 lo sigue', () => {
    item('g').focus()
    key(item('g'), 'ArrowDown')
    expect(document.activeElement).toBe(item('c1'))
    expect(item('c1').getAttribute('tabindex')).toBe('0')
    expect(item('g').getAttribute('tabindex')).toBe('-1')
  })

  it('izquierda en un hijo va al grupo; izquierda en el grupo abierto lo pliega (clic)', () => {
    item('c2').focus()
    key(item('c2'), 'ArrowLeft')
    expect(document.activeElement).toBe(item('g'))
    const click = vi.fn()
    item('g').addEventListener('click', click)
    key(item('g'), 'ArrowLeft')
    expect(click).toHaveBeenCalledOnce()
  })

  it('Espacio en una fila que no es botón hace clic', () => {
    const click = vi.fn()
    item('c1').addEventListener('click', click)
    key(item('c1'), ' ')
    expect(click).toHaveBeenCalledOnce()
  })

  it('las teclas de un botón de adentro no son del árbol', () => {
    item('x').focus()
    expect(key(item('x'), 'ArrowDown').defaultPrevented).toBe(false)
  })

  it('la tecla Menú y Shift+F10 piden el menú contextual de la fila', () => {
    const menu = vi.fn()
    item('c1').addEventListener('contextmenu', menu)
    key(item('c1'), 'ContextMenu')
    key(item('c1'), 'F10', { shiftKey: true })
    key(item('c1'), 'F10')
    expect(menu).toHaveBeenCalledTimes(2)
  })
})
