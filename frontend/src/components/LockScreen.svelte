<script lang="ts">
  import { onMount } from 'svelte'
  import { t } from '../lib/i18n'
  import { api, errorText } from '../lib/api'
  import { focusTrap } from '../lib/focustrap'
  import { helloWhenVisible, lockState, refreshLock, unlockWithHello } from '../lib/lock'
  import { holdOverlay } from '../lib/overlay'
  import LogoMark from '../lib/LogoMark.svelte'

  /** Alto de la barra de título, que queda libre para poder minimizar, mover o cerrar la ventana. */
  export let top = 0

  let root: HTMLElement
  let input: HTMLInputElement | undefined
  let password = ''
  let error = ''
  let busy = false
  let helloBusy = false
  let helloTried = false

  // Hello se lanza una sola vez por bloqueo, apenas se sabe si está permitido; si se cancela, queda la contraseña.
  // Si osat no está al frente (minimizado, o se bloqueó por inactividad mientras se usaba otra cosa), espera a que
  // se vuelva a la ventana: el diálogo de Hello no tiene que aparecer minimizado ni encima de otra aplicación.
  let stopHelloWait: (() => void) | undefined
  $: if ($lockState && !helloTried) {
    helloTried = true
    if ($lockState.helloEnabled) stopHelloWait = helloWhenVisible(hello)
  }
  // El campo aparece cuando llega el estado: ahí toma el foco.
  $: input?.focus()

  async function unlock() {
    if (busy || !password) return
    busy = true
    error = ''
    try {
      await api.lockUnlock(password)
      await refreshLock()
    } catch (e) {
      error = errorText(e)
      input?.focus()
    } finally {
      password = ''
      busy = false
    }
  }

  async function hello() {
    if (helloBusy) return
    helloBusy = true
    try {
      await unlockWithHello()
    } catch {
      // cancelar o que Hello falle no se avisa: se sigue con la contraseña
    } finally {
      helloBusy = false
      input?.focus()
    }
  }

  // Mientras está bloqueado ninguna tecla llega a lo que hay debajo (una terminal, un escritorio remoto,
  // cualquier control): en la fase de captura de window se frena todo lo que no es de esta pantalla.
  // Los atajos de App.svelte lo respetan mirando el store locked.
  const KEY_EVENTS = ['keydown', 'keyup', 'keypress', 'beforeinput', 'paste'] as const
  onMount(() => {
    // Los sitios web son vistas nativas, encima del HTML: se ocultan mientras dure el bloqueo.
    const release = holdOverlay()
    const swallow = (e: Event) => {
      if (root.contains(e.target as Node)) return
      e.preventDefault()
      e.stopPropagation()
    }
    // Si algo de afuera toma el foco (un diálogo que se abre solo, por ejemplo), vuelve acá.
    const refocus = (e: FocusEvent) => {
      if (!root.contains(e.target as Node)) (input ?? root).focus()
    }
    for (const ev of KEY_EVENTS) window.addEventListener(ev, swallow, true)
    window.addEventListener('focusin', refocus, true)
    return () => {
      release()
      stopHelloWait?.()
      for (const ev of KEY_EVENTS) window.removeEventListener(ev, swallow, true)
      window.removeEventListener('focusin', refocus, true)
    }
  })
</script>

<!-- Un clic en el fondo no le saca el foco al campo. Lo de afuera (que sigue andando por detrás) no se desmonta. -->
<div
  class="lock"
  bind:this={root}
  role="presentation"
  style="top: {top}px"
  use:focusTrap={'input'}
  on:mousedown={(e) => {
    if (!(e.target instanceof Element && e.target.closest('input, button'))) e.preventDefault()
  }}
  on:keydown|stopPropagation
  on:keyup|stopPropagation
  on:keypress|stopPropagation
>
  <div class="card" role="dialog" aria-modal="true" aria-labelledby="lock-title">
    <LogoMark size={56} />
    <h1 id="lock-title">{$t('lock.title')}</h1>
    {#if $lockState}
      <form on:submit|preventDefault={unlock}>
        <label>
          {$t('lock.password')}
          <input type="password" bind:this={input} bind:value={password} readonly={busy} autocomplete="off" spellcheck="false" />
        </label>
        {#if error}<p class="error" role="alert">{error}</p>{/if}
        <button type="submit" class="primary" disabled={busy || !password}>{$t('lock.unlock')}</button>
        {#if $lockState.helloEnabled}
          <button type="button" disabled={helloBusy} on:click={hello}>{$t('lock.useHello')}</button>
        {/if}
      </form>
    {/if}
  </div>
</div>

<style>
  .lock {
    position: fixed;
    inset: 0;
    z-index: 5000; /* por encima de los diálogos (900), los menús y los avisos */
    display: grid;
    place-items: center;
    background: var(--bg);
    color: var(--text);
  }
  .card {
    width: 300px;
    max-width: 92vw;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 14px;
  }
  h1 {
    margin: 0;
    font-size: 15px;
    font-weight: 600;
  }
  form {
    width: 100%;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  label {
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 12px;
    color: var(--text-2);
  }
  .error {
    margin: 0;
    color: var(--danger);
  }
</style>
