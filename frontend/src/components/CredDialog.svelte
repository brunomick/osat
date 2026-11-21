<script lang="ts">
  import { focusTrap } from '../lib/focustrap'
  import { t } from '../lib/i18n'
  import { createEventDispatcher, onDestroy, onMount } from 'svelte'
  import { api, errorText, type Cred } from '../lib/api'
  import { holdOverlay } from '../lib/overlay'
  import { reloadCreds, toast } from '../lib/stores'

  onDestroy(holdOverlay())

  // Crear o editar una credencial para los sitios. Al editar, la contraseña vacía deja la guardada.
  /** La credencial a editar; sin ella, una nueva. */
  export let cred: Cred | undefined = undefined

  const dispatch = createEventDispatcher<{ close: void; saved: Cred }>()

  let label = cred?.label ?? ''
  let account = ''
  let user = ''
  let password = ''
  let loading = !!cred
  let saving = false
  let labelInput: HTMLInputElement

  onMount(async () => {
    labelInput?.focus()
    if (!cred) return
    try {
      const d = await api.credGet(cred.id)
      account = d.account
      user = d.user
    } catch (e) {
      toast(errorText(e), 'error')
      dispatch('close')
    } finally {
      loading = false
    }
  })

  async function save() {
    saving = true
    try {
      const c = await api.credSave({ id: cred?.id ?? '', label: label.trim(), account, user, password, keepPassword: !!cred && password === '' })
      await reloadCreds()
      dispatch('saved', c)
      dispatch('close')
    } catch (e) {
      toast(errorText(e), 'error')
    } finally {
      saving = false
    }
  }
</script>

<div class="backdrop" role="presentation" on:mousedown|self={() => dispatch('close')} on:keydown={(e) => e.key === 'Escape' && dispatch('close')}>
  <div class="modal" role="dialog" aria-modal="true" use:focusTrap aria-labelledby="cred-dialog-title">
    <h2 id="cred-dialog-title">{cred ? $t('site.credEdit') : $t('site.credNew')}</h2>
    <form on:submit|preventDefault={save}>
      <label>
        {$t('site.credLabel')}
        <input bind:this={labelInput} bind:value={label} placeholder={$t('site.credLabelPlaceholder')} spellcheck="false" required />
      </label>
      <label>
        {$t('site.credAccount')}
        <input bind:value={account} spellcheck="false" autocomplete="off" disabled={loading} />
      </label>
      <label>
        {$t('site.credUser')}
        <input bind:value={user} spellcheck="false" autocomplete="off" disabled={loading} />
      </label>
      <label>
        {$t('site.credPassword')}
        <input type="password" bind:value={password} placeholder={cred ? $t('site.credKeep') : ''} autocomplete="new-password" disabled={loading} />
      </label>
      <span class="dim">{$t('site.credHelp')}</span>
      <footer>
        <span class="spacer" />
        <button type="button" on:click={() => dispatch('close')}>{$t('common.cancel')}</button>
        <button class="primary" type="submit" disabled={loading || saving || !label.trim()}>{saving ? $t('form.saving') : $t('common.save')}</button>
      </footer>
    </form>
  </div>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    z-index: 400;
  }
  .modal {
    width: 420px;
    max-width: 94vw;
    max-height: 92vh;
    overflow: auto;
    background: var(--bg-2);
    border: 1px solid var(--border);
    padding: 20px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  h2 {
    margin: 0;
    font-size: 16px;
    font-weight: 600;
  }
  form {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  label {
    display: flex;
    flex-direction: column;
    gap: 4px;
    color: var(--text-2);
    font-size: 12px;
  }
  label input {
    color: var(--text);
    font-size: 13px;
  }
  .dim {
    color: var(--text-3);
    font-size: 11px;
  }
  footer {
    display: flex;
    gap: 8px;
    margin-top: 4px;
  }
  .spacer {
    flex: 1;
  }
</style>
