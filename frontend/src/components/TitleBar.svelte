<script lang="ts">
  import { onMount } from 'svelte'
  import Wordmark from '../lib/Wordmark.svelte'
  import { aboutOpen } from '../lib/stores'
  import { locked } from '../lib/lock'
  import { t } from '../lib/i18n'
  import { isMac } from '../lib/platform'

  // Barra superior propia (la ventana es Frameless, salvo en macOS). Se arrastra por la zona
  // con --wails-draggable: drag; doble clic maximiza/restaura.
  let maximised = false

  const rt = () => window.runtime

  async function sync() {
    maximised = !!(await rt()?.WindowIsMaximised?.())
  }

  async function toggleMax() {
    rt()?.WindowToggleMaximise?.()
    setTimeout(sync, 100)
  }

  onMount(() => {
    sync()
    window.addEventListener('resize', sync)
    return () => window.removeEventListener('resize', sync)
  })
</script>

<!-- En macOS la barra del sistema va oculta (mac.TitleBarHidden en main.go) pero
     quedan los semáforos nativos arriba a la izquierda: dejamos ~78px libres a la
     izquierda y no mostramos el nav propio de minimizar/maximizar/cerrar. -->
<header role="presentation" class:mac={isMac()} style="--wails-draggable: drag" on:dblclick={toggleMax}>
  <button class="brand" style="--wails-draggable: no-drag" disabled={$locked} title={$t('titlebar.about')} on:click={() => aboutOpen.set(true)} on:dblclick|stopPropagation>
    <Wordmark height={11} />
  </button>
  <span class="title" />
  {#if !isMac()}
    <nav style="--wails-draggable: no-drag" on:dblclick|stopPropagation>
      <button title={$t('titlebar.minimize')} on:click={() => rt()?.WindowMinimise?.()}>&#x2013;</button>
      <button title={maximised ? $t('titlebar.restore') : $t('titlebar.maximize')} on:click={toggleMax}>{maximised ? '❐' : '☐'}</button>
      <button class="close" title={$t('common.close')} on:click={() => rt()?.Quit?.()}>&#x2715;</button>
    </nav>
  {/if}
</header>

<style>
  header {
    display: flex;
    align-items: center;
    height: 30px;
    flex: none;
    background: var(--rail);
    border-bottom: 1px solid var(--border);
  }
  /* Hueco a la izquierda para los semáforos nativos de macOS. */
  header.mac {
    padding-left: 78px;
  }
  .title {
    flex: 1;
    align-self: stretch;
  }
  .brand {
    display: flex;
    align-items: center;
    width: auto;
    padding: 0 10px;
  }
  nav {
    display: flex;
    height: 100%;
  }
  button {
    width: 46px;
    height: 100%;
    border: 0;
    background: transparent;
    color: inherit;
  }
  button:hover {
    background: var(--bg-4);
  }
  .close:hover {
    background: var(--win-close);
    color: var(--on-win-close);
  }
</style>
