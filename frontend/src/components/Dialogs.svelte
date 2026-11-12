<script lang="ts">
  import { t } from '../lib/i18n'
  import { focusTrap } from '../lib/focustrap'
  import { onDestroy, tick } from 'svelte'
  import { dialog } from '../lib/dialog'
  import { overlayHolder } from '../lib/overlay'

  let secret = ''
  let save = true
  let value = ''
  let answers: string[] = []
  let input: HTMLInputElement | undefined
  let okBtn: HTMLButtonElement | undefined
  let form: HTMLFormElement | undefined

  // La vista nativa de un sitio se dibuja encima del HTML: con un diálogo abierto se oculta.
  const hold = overlayHolder()
  $: hold(!!$dialog)
  onDestroy(() => hold(false))

  $: if ($dialog) init()

  async function init() {
    secret = ''
    save = true
    value = $dialog?.type === 'prompt' ? $dialog.value : ''
    answers = $dialog?.type === 'questions' ? $dialog.questions.map((q) => q.value ?? '') : []
    await tick()
    const first = form?.querySelector('input:not([type=checkbox])') as HTMLInputElement | null
    if (first) {
      first.focus()
      const end = $dialog?.type === 'prompt' ? $dialog.selectEnd : undefined
      if (end !== undefined) first.setSelectionRange(0, end)
      else first.select()
    } else okBtn?.focus()
  }

  function ok() {
    const d = $dialog
    if (!d) return
    if (d.type === 'secret') d.resolve({ secret, save })
    else if (d.type === 'confirm') d.resolve(true)
    else if (d.type === 'questions') d.resolve(answers)
    else if (d.type === 'choice') d.resolve(d.choices.find((c) => c.kind === 'primary')?.value ?? d.choices[0].value)
    else d.resolve(value.trim() || null)
  }

  function cancel() {
    const d = $dialog
    if (!d) return
    if (d.type === 'confirm') d.resolve(false)
    else d.resolve(null)
  }
</script>

{#if $dialog}
  <div class="backdrop" role="presentation" on:keydown={(e) => e.key === 'Escape' && cancel()}>
    <form class="dlg" bind:this={form} on:submit|preventDefault={ok} role="dialog" aria-modal="true" use:focusTrap aria-labelledby="dlg-title">
      <h3 id="dlg-title" class:danger={$dialog.type === 'confirm' && $dialog.danger}>{$dialog.title}</h3>
      {#if $dialog.message}<p>{$dialog.message}</p>{/if}

      {#if $dialog.type === 'secret'}
        <label>
          {$dialog.label}
          <input type="password" bind:this={input} bind:value={secret} autocomplete="off" />
        </label>
        {#if $dialog.allowSave}
          <label class="check"><input type="checkbox" bind:checked={save} /> {$t('dialog.remember')}</label>
        {/if}
      {:else if $dialog.type === 'prompt'}
        <input bind:this={input} bind:value spellcheck="false" />
      {:else if $dialog.type === 'questions'}
        {#each $dialog.questions as q, i}
          <label>
            {q.text}
            {#if q.echo}
              <input bind:value={answers[i]} autocomplete="off" spellcheck="false" />
            {:else}
              <input type="password" bind:value={answers[i]} autocomplete="off" />
            {/if}
          </label>
        {/each}
      {:else if $dialog.type === 'choice' && $dialog.list?.length}
        <ul class="mono">
          {#each $dialog.list as item}<li>{item}</li>{/each}
        </ul>
      {:else if $dialog.type === 'confirm' && $dialog.details}
        <dl>
          {#each $dialog.details as [k, v]}
            <dt>{k}</dt>
            <dd class="mono">{v}</dd>
          {/each}
        </dl>
      {/if}

      <footer>
        <button type="button" on:click={cancel}>{$t('common.cancel')}</button>
        {#if $dialog.type === 'choice'}
          {#each $dialog.choices as c}
            {#if c.kind === 'primary'}
              <button bind:this={okBtn} type="submit" class="primary">{c.label}</button>
            {:else}
              <button type="button" class={c.kind ?? ''} on:click={() => $dialog?.type === 'choice' && $dialog.resolve(c.value)}>
                {c.label}
              </button>
            {/if}
          {/each}
        {:else}
          <button
            bind:this={okBtn}
            type="submit"
            class={$dialog.type === 'confirm' && $dialog.danger ? 'danger' : 'primary'}
          >
            {$dialog.type === 'secret' ? $dialog.okText ?? $t('dialog.connect') : $dialog.type === 'questions' ? $t('dialog.send') : $dialog.okText}
          </button>
        {/if}
      </footer>
    </form>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    z-index: 900;
  }
  .dlg {
    width: 440px;
    max-width: 92vw;
    background: var(--bg-2);
    border: 1px solid var(--border);
    padding: 18px 20px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  h3 {
    margin: 0;
    font-size: 15px;
  }
  h3.danger {
    color: var(--danger);
  }
  p {
    margin: 0;
    color: var(--text-2);
    user-select: text;
    white-space: pre-line;
  }
  label {
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 12px;
    color: var(--text-2);
  }
  label.check {
    flex-direction: row;
    align-items: center;
    gap: 8px;
    color: var(--text);
  }
  dl {
    margin: 0;
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 6px 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    padding: 10px 12px;
    user-select: text;
  }
  dt {
    color: var(--text-3);
  }
  dd {
    margin: 0;
    word-break: break-all;
    font-size: 12px;
  }
  ul {
    margin: 0;
    padding: 8px 12px 8px 28px;
    max-height: 160px;
    overflow: auto;
    background: var(--bg);
    border: 1px solid var(--border);
    font-size: 12px;
    user-select: text;
  }
  footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
</style>
