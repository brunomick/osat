// Navegación con el teclado de los árboles de la barra lateral (role="tree").

export interface TreeItem {
  /** aria-level (1 = raíz). */
  level: number
  /** true/false = grupo abierto/plegado; undefined = no se pliega. */
  expanded?: boolean
}
export type TreeMove = { focus: number } | { toggle: true } | null

/** A qué fila va el foco (o si se abre/pliega la actual) al apretar `key` sobre la fila `i`. */
export function treeMove(items: TreeItem[], i: number, key: string): TreeMove {
  const n = items.length
  const cur = items[i]
  if (!cur) return null
  switch (key) {
    case 'ArrowDown':
      return i + 1 < n ? { focus: i + 1 } : null
    case 'ArrowUp':
      return i > 0 ? { focus: i - 1 } : null
    case 'Home':
      return i > 0 ? { focus: 0 } : null
    case 'End':
      return i < n - 1 ? { focus: n - 1 } : null
    case 'ArrowRight':
      if (cur.expanded === false) return { toggle: true }
      if (cur.expanded === true && items[i + 1]?.level > cur.level) return { focus: i + 1 }
      return null
    case 'ArrowLeft': {
      if (cur.expanded === true) return { toggle: true }
      for (let j = i - 1; j >= 0; j--) if (items[j].level < cur.level) return { focus: j }
      return null
    }
  }
  return null
}

/** Abre el menú contextual de `el` como si se hubiera hecho clic derecho sobre él (tecla Menú o Shift+F10). */
export function contextMenuAt(el: HTMLElement) {
  const r = el.getBoundingClientRect()
  el.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: r.left + 24, clientY: r.top + r.height / 2 }))
}

export const isMenuKey = (e: KeyboardEvent) => e.key === 'ContextMenu' || (e.key === 'F10' && e.shiftKey)

/**
 * Acción para un role="tree": foco itinerante (una sola fila con tabindex 0), flechas, Espacio y menú contextual.
 * Las filas son los [role="treeitem"] de adentro, con aria-level y, si se pliegan, aria-expanded.
 */
export function treeNav(node: HTMLElement) {
  const rows = () => [...node.querySelectorAll<HTMLElement>('[role="treeitem"]')]
  let current: HTMLElement | null = null

  // Deja una sola fila en el orden de Tab: la última enfocada, o la primera.
  function sync() {
    const list = rows()
    if (!list.length) return
    const keep = current && list.includes(current) ? current : list[0]
    for (const el of list) {
      const want = el === keep ? '0' : '-1'
      if (el.getAttribute('tabindex') !== want) el.setAttribute('tabindex', want)
    }
  }

  function onFocusin(e: FocusEvent) {
    const el = (e.target as HTMLElement).closest<HTMLElement>('[role="treeitem"]')
    if (el && el === e.target) {
      current = el
      sync()
    }
  }

  function onKeydown(e: KeyboardEvent) {
    const el = e.target as HTMLElement
    if (el.getAttribute('role') !== 'treeitem') return // teclas de un botón o campo de adentro: no son del árbol
    if (isMenuKey(e)) {
      e.preventDefault()
      contextMenuAt(el)
      return
    }
    if (e.key === ' ' && el.tagName !== 'BUTTON') {
      e.preventDefault()
      el.click()
      return
    }
    const list = rows()
    const items: TreeItem[] = list.map((r) => ({
      level: Number(r.getAttribute('aria-level')) || 1,
      expanded: r.hasAttribute('aria-expanded') ? r.getAttribute('aria-expanded') === 'true' : undefined,
    }))
    const move = treeMove(items, list.indexOf(el), e.key)
    if (!move) return
    e.preventDefault()
    if ('toggle' in move) el.click()
    else list[move.focus].focus()
  }

  const mo = new MutationObserver(sync)
  mo.observe(node, { childList: true, subtree: true })
  node.addEventListener('focusin', onFocusin)
  node.addEventListener('keydown', onKeydown)
  sync()

  return {
    destroy() {
      mo.disconnect()
      node.removeEventListener('focusin', onFocusin)
      node.removeEventListener('keydown', onKeydown)
    },
  }
}
