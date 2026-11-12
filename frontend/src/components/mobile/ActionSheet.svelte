<script lang="ts" context="module">
  export interface SheetItem {
    label: string
    icon: string
    danger?: boolean
    action: () => void
  }
</script>

<script lang="ts">
  // Android: las opciones de algo (lo que en escritorio es el clic derecho), en una hoja que sube desde abajo.
  import { createEventDispatcher, onMount } from 'svelte'
  import Icon from '../../lib/Icon.svelte'

  export let title = ''
  export let items: SheetItem[]

  const dispatch = createEventDispatcher<{ close: void }>()
  // Con el foco en la hoja, Esc (y Atrás de Android, que manda Esc) la cierra.
  let el: HTMLDivElement
  onMount(() => el.focus())
  const pick = (it: SheetItem) => {
    dispatch('close')
    it.action()
  }
</script>

<div class="scrim" role="presentation" on:click={() => dispatch('close')}>
  <div class="sheet" role="menu" tabindex="-1" bind:this={el} on:click|stopPropagation on:keydown={(e) => e.key === 'Escape' && dispatch('close')}>
    <div class="grip" />
    {#if title}<div class="title">{title}</div>{/if}
    {#each items as it}
      <button role="menuitem" class:danger={it.danger} on:click={() => pick(it)}>
        <Icon name={it.icon} size={18} />
        <span>{it.label}</span>
      </button>
    {/each}
  </div>
</div>

<style>
  .scrim {
    position: absolute;
    inset: 0;
    z-index: 60;
    background: rgba(0, 0, 0, 0.45);
    display: flex;
    flex-direction: column;
    justify-content: flex-end;
  }
  .sheet {
    outline: none;
    background: var(--bg-2);
    border-radius: 16px 16px 0 0;
    padding: 8px 0 12px;
  }
  .grip {
    width: 36px;
    height: 4px;
    border-radius: 2px;
    background: var(--border);
    margin: 0 auto 8px;
  }
  .title {
    padding: 4px 20px 8px;
    color: var(--text-3);
    font-size: 13px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  button {
    display: flex;
    align-items: center;
    gap: 14px;
    width: 100%;
    padding: 14px 20px;
    font-size: 15px;
    text-align: left;
    border: 0;
    background: none;
  }
  button.danger {
    color: var(--danger);
  }
</style>
