<script context="module" lang="ts">
  import type { TermTheme } from '../lib/termthemes'

  /** Un tema para elegir: su muestra sale de la paleta (sin paleta, mitad osat oscuro y mitad claro). */
  export type ThemeChoice = { id: string; name: string; palette: TermTheme | null }
</script>

<script lang="ts">
  // Tarjetas para elegir un tema (Ajustes → Apariencia y Terminal): una muestra de cada uno con su fondo, su texto
  // y cinco colores. Un clic o las flechas del teclado lo eligen (quien la usa lo aplica al momento); al pasar el
  // mouse avisa cuál se está mirando, para mostrarlo sin elegirlo.
  import { createEventDispatcher } from 'svelte'

  export let groups: { title: string; choices: ThemeChoice[] }[]
  export let value: string
  export let label: string

  const dispatch = createEventDispatcher<{ pick: string; hover: string | null }>()
  const cards: Record<string, HTMLButtonElement> = {}

  $: order = groups.flatMap((g) => g.choices.map((c) => c.id)) // el orden en que se recorren con las flechas

  function key(e: KeyboardEvent) {
    const i = order.indexOf(value)
    const k = e.key
    const n = k === 'ArrowRight' || k === 'ArrowDown' ? i + 1 : k === 'ArrowLeft' || k === 'ArrowUp' ? i - 1 : k === 'Home' ? 0 : k === 'End' ? order.length - 1 : null
    if (n === null) return
    e.preventDefault()
    const next = order[(n + order.length) % order.length]
    dispatch('pick', next)
    cards[next]?.focus()
  }
</script>

<div class="themes" role="radiogroup" tabindex="-1" aria-label={label} on:mouseleave={() => dispatch('hover', null)}>
  {#each groups as g}
    {#if g.title}<span class="group">{g.title}</span>{/if}
    <div class="grid">
      {#each g.choices as c (c.id)}
        {@const on = value === c.id}
        {@const p = c.palette}
        <button
          type="button"
          class="card"
          class:on
          role="radio"
          aria-checked={on}
          tabindex={on ? 0 : -1}
          bind:this={cards[c.id]}
          on:click={() => dispatch('pick', c.id)}
          on:keydown={key}
          on:mouseenter={() => dispatch('hover', c.id)}
        >
          {#if p}
            <span class="swatch" style:background={p.background}>
              <span class="ln" style:background={p.foreground} />
              <span class="dots">
                {#each [p.red, p.green, p.yellow, p.blue, p.magenta] as color}<i style:background={color} />{/each}
              </span>
            </span>
          {:else}
            <span class="swatch split" />
          {/if}
          <span class="name">{c.name}</span>
        </button>
      {/each}
    </div>
  {/each}
</div>

<style>
  .themes {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .group {
    font-size: 11px;
    color: var(--text-3);
    margin-top: 4px;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(112px, 1fr));
    gap: 8px;
  }
  .card {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 6px;
    background: var(--bg-2);
    border: 1px solid var(--border);
    color: var(--text-2);
    font-size: 12px;
    text-align: left;
  }
  .card:hover {
    border-color: var(--text-3);
  }
  .card.on {
    border-color: var(--signal);
    box-shadow: inset 0 0 0 1px var(--signal);
    color: var(--text);
  }
  .swatch {
    height: 40px;
    display: flex;
    flex-direction: column;
    justify-content: center;
    gap: 7px;
    padding: 0 8px;
    border: 1px solid var(--border);
  }
  .swatch.split {
    background: linear-gradient(90deg, #0a0a0a 50%, #ffffff 50%); /* osat oscuro y claro */
  }
  .ln {
    width: 70%;
    height: 4px;
    border-radius: 2px;
  }
  .dots {
    display: flex;
    gap: 4px;
  }
  .dots i {
    width: 8px;
    height: 8px;
    border-radius: 50%;
  }
  .name {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
