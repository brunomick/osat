// Acción de Svelte para los diálogos: mantiene el foco adentro mientras están abiertos
// (Tab y Shift+Tab dan la vuelta) y lo devuelve al elemento anterior al cerrarse.

const FOCUSABLE = 'a[href], button, input:not([type="hidden"]), select, textarea, [tabindex]'

/** Los controles que reciben Tab dentro de `node`, en orden. */
export function focusables(node: HTMLElement): HTMLElement[] {
  return [...node.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(
    (el) => el.tabIndex >= 0 && !el.matches(':disabled') && !el.closest('[hidden]'),
  )
}

/**
 * `initial`: selector CSS del control que arranca con el foco. Sin él, el primer control
 * (si el diálogo ya enfocó algo adentro, se respeta).
 */
export function focusTrap(node: HTMLElement, initial?: string) {
  const prev = document.activeElement as HTMLElement | null

  if (!node.contains(document.activeElement)) {
    const target = (initial && node.querySelector<HTMLElement>(initial)) || focusables(node)[0]
    if (target) target.focus()
    else {
      node.tabIndex = -1 // sin controles: el foco igual queda dentro del diálogo
      node.focus()
    }
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key !== 'Tab') return
    const list = focusables(node)
    if (!list.length) return e.preventDefault()
    const first = list[0]
    const last = list[list.length - 1]
    const active = document.activeElement
    if (e.shiftKey && (active === first || !node.contains(active))) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && (active === last || !node.contains(active))) {
      e.preventDefault()
      first.focus()
    }
  }
  node.addEventListener('keydown', onKeydown)

  return {
    destroy() {
      node.removeEventListener('keydown', onKeydown)
      // Si lo que abrió el diálogo ya no existe, el foco queda donde esté.
      if (prev && prev.isConnected) prev.focus()
    },
  }
}
