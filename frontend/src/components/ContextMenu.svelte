<script context="module" lang="ts">
  export interface MenuItem {
    label: string
    icon?: string
    danger?: boolean
    disabled?: boolean
    separator?: boolean
    /** Atajo de teclado, a la derecha ("Ctrl+C"). */
    hint?: string
    action?: () => void
  }
</script>

<script lang="ts">
  import { createEventDispatcher, onDestroy, onMount, tick } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { holdOverlay } from '../lib/overlay'

  export let x: number
  export let y: number
  export let items: MenuItem[]

  const dispatch = createEventDispatcher<{ close: void }>()
  onDestroy(holdOverlay()) // el menú tiene que quedar encima de la vista nativa de un sitio
  let el: HTMLDivElement
  let left = x
  let top = y

  // Foco: al primer ítem habilitado al abrir; al cerrar vuelve a donde estaba.
  const prevFocus = document.activeElement as HTMLElement | null
  const enabled = () => [...el.querySelectorAll<HTMLButtonElement>('.mi:not(:disabled)')]

  onMount(() => {
    tick().then(() => {
      const r = el.getBoundingClientRect()
      left = Math.min(x, window.innerWidth - r.width - 4)
      top = Math.min(y, window.innerHeight - r.height - 4)
      enabled()[0]?.focus()
    })
    const close = (e: Event) => {
      if (e instanceof KeyboardEvent && e.key !== 'Escape') return
      if (e instanceof MouseEvent && el.contains(e.target as Node)) return
      dispatch('close')
    }
    window.addEventListener('mousedown', close, true)
    window.addEventListener('keydown', close, true)
    window.addEventListener('blur', close)
    return () => {
      window.removeEventListener('mousedown', close, true)
      window.removeEventListener('keydown', close, true)
      window.removeEventListener('blur', close)
    }
  })

  onDestroy(() => {
    // Si la acción elegida ya llevó el foco a otro lado (un diálogo, un campo), no se lo saca.
    const a = document.activeElement
    if ((!a || a === document.body) && prevFocus?.isConnected) prevFocus.focus()
  })

  // Flechas (saltan separadores y deshabilitados), Inicio y Fin. Enter y Espacio activan el botón enfocado.
  function onKeydown(e: KeyboardEvent) {
    const list = enabled()
    const i = list.indexOf(document.activeElement as HTMLButtonElement)
    let to = -1
    if (e.key === 'ArrowDown') to = (i + 1) % list.length
    else if (e.key === 'ArrowUp') to = (i <= 0 ? list.length : i) - 1
    else if (e.key === 'Home') to = 0
    else if (e.key === 'End') to = list.length - 1
    else if (e.key === 'Tab') to = e.shiftKey ? (i <= 0 ? list.length : i) - 1 : (i + 1) % list.length
    else return
    e.preventDefault()
    list[to]?.focus()
  }

  function run(it: MenuItem) {
    if (it.disabled) return
    dispatch('close')
    it.action?.()
  }
</script>

<div class="menu" bind:this={el} style="left:{left}px;top:{top}px" role="menu" tabindex="-1" on:keydown={onKeydown}>
  {#each items as it}
    {#if it.separator}
      <div class="sep" />
    {:else}
      <button class="mi" class:danger={it.danger} disabled={it.disabled} role="menuitem" on:click={() => run(it)}>
        <span class="ic">{#if it.icon}<Icon name={it.icon} size={14} />{/if}</span>
        {it.label}
        {#if it.hint}<span class="kbd">{it.hint}</span>{/if}
      </button>
    {/if}
  {/each}
</div>

<style>
  .menu {
    position: fixed;
    z-index: 1000;
    min-width: 180px;
    background: var(--bg-3);
    border: 1px solid var(--border);
    padding: 4px;
  }
  .mi {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    text-align: left;
    border: 0;
    background: transparent;
    padding: 6px 8px;
  }
  .mi:hover:not(:disabled) {
    background: var(--accent) !important;
    color: var(--on-accent);
  }
  .mi.danger {
    color: var(--danger);
  }
  .mi.danger:hover {
    background: var(--danger) !important;
    color: var(--on-danger);
  }
  .ic {
    width: 14px;
    display: inline-flex;
  }
  .kbd {
    margin-left: auto;
    padding-left: 16px;
    font-size: 11px;
    color: var(--text-3);
  }
  .mi:hover:not(:disabled) .kbd {
    color: inherit;
  }
  .sep {
    height: 1px;
    background: var(--border);
    margin: 4px 0;
  }
</style>
