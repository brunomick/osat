<script lang="ts">
  import { t, tr } from '../lib/i18n'
  import { createEventDispatcher, onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { api, clipboardSet, errorText, type Cred, type CredData } from '../lib/api'
  import { confirmDialog } from '../lib/dialog'
  import { locked } from '../lib/lock'
  import { copySecret } from '../lib/secretclip'
  import { creds, reloadCreds, toast } from '../lib/stores'
  import CredDialog from './CredDialog.svelte'

  // Credenciales para los sitios (un baúl único, sin relación con el sitio abierto), en una franja entre la
  // barra y la página: no va encima, así la página sigue visible para pegar lo copiado. Los datos se leen del
  // almacén del sistema recién al mostrarlos.

  const dispatch = createEventDispatcher<{ close: void }>()

  let selected = ''
  let data: CredData | null = null
  let showPassword = false
  let error = ''
  let seq = 0
  /** El diálogo abierto: { cred } edita, { cred: undefined } crea; null = cerrado. */
  let dialog: { cred: Cred | undefined } | null = null

  onMount(() => {
    reloadCreds().catch((e) => (error = errorText(e)))
  })

  $: shown = [...$creds].sort((a, b) => a.label.localeCompare(b.label, undefined, { sensitivity: 'base' }))
  // Si la elegida se borró (o todavía no hay ninguna), la primera.
  $: if (!shown.some((c) => c.id === selected)) selected = shown[0]?.id ?? ''
  $: current = $creds.find((c) => c.id === selected)
  // Al bloquearse osat no queda nada a la vista.
  $: if ($locked) (data = null), dispatch('close')
  // $creds va para volver a leer si se editó la elegida.
  $: load(selected, $creds)

  async function load(id: string, ..._changed: unknown[]) {
    const my = ++seq
    data = null
    error = ''
    showPassword = false
    if (!id) return
    try {
      const d = await api.credGet(id)
      if (my === seq) data = d
    } catch (e) {
      if (my === seq) error = errorText(e)
    }
  }

  async function copy(what: string, value: string, secret: boolean) {
    try {
      if (secret) {
        await copySecret(value)
        toast(tr('site.passwordCopied'), 'success')
      } else {
        await clipboardSet(value)
        toast(tr('site.copied', { what }), 'success')
      }
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function remove(c: Cred) {
    const ok = await confirmDialog({ title: tr('site.credDelete'), message: tr('site.credDeleteAsk', { label: c.label }), okText: tr('common.delete'), danger: true })
    if (!ok) return
    try {
      await api.credDelete(c.id)
      await reloadCreds()
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  function saved(e: CustomEvent<Cred>) {
    selected = e.detail.id
  }

  $: fields = data
    ? [
        { key: 'account', label: $t('site.credAccount'), value: data.account, secret: false },
        { key: 'user', label: $t('site.credUser'), value: data.user, secret: false },
        { key: 'password', label: $t('site.credPassword'), value: data.password, secret: true },
      ].filter((f) => f.value) // los que no se cargaron no se muestran
    : []
</script>

<div class="creds" role="region" aria-label={$t('site.creds')}>
  <span class="pick">
    <Icon name="key" size={14} />
    {#if shown.length}
      <select bind:value={selected} aria-label={$t('site.credLabel')}>
        {#each shown as c (c.id)}<option value={c.id}>{c.label}</option>{/each}
      </select>
    {:else}
      <span class="muted">{$t('site.credsEmpty')}</span>
    {/if}
  </span>


  {#if error}
    <span class="err">{error}</span>
  {:else}
    {#each fields as f (f.key)}
      <span class="field">
        <span class="lbl">{f.label}</span>
        {#if f.secret && !showPassword}
          <span class="val mono">••••••••</span>
        {:else}
          <span class="val mono" title={f.value}>{f.value}</span>
        {/if}
        {#if f.secret}
          <button
            type="button"
            class="icon"
            class:on={showPassword}
            title={showPassword ? $t('site.hide') : $t('site.show')}
            aria-label={showPassword ? $t('site.hide') : $t('site.show')}
            aria-pressed={showPassword}
            on:click={() => (showPassword = !showPassword)}
          >
            <Icon name="eye" size={14} />
          </button>
        {/if}
        <button
          type="button"
          class="icon"
          title={$t('site.copy') + ': ' + f.label}
          aria-label={$t('site.copy') + ': ' + f.label}
          on:click={() => copy(f.label, f.value, f.secret)}
        >
          <Icon name="copy" size={14} />
        </button>
      </span>
    {/each}
  {/if}

  <span class="actions">
    {#if current}
      <button type="button" class="icon" title={$t('site.credEdit')} aria-label={$t('site.credEdit')} on:click={() => (dialog = { cred: current })}>
        <Icon name="edit" size={14} />
      </button>
      <button type="button" class="icon" title={$t('site.credDelete')} aria-label={$t('site.credDelete')} on:click={() => current && remove(current)}>
        <Icon name="trash" size={14} />
      </button>
    {/if}
    <button type="button" class="icon" title={$t('site.credNew')} aria-label={$t('site.credNew')} on:click={() => (dialog = { cred: undefined })}>
      <Icon name="plus" size={14} />
    </button>
    <button type="button" class="icon" title={$t('site.credsClose')} aria-label={$t('site.credsClose')} on:click={() => dispatch('close')}>
      <Icon name="x" size={14} />
    </button>
  </span>
</div>

{#if dialog}
  <CredDialog cred={dialog.cred} on:saved={saved} on:close={() => (dialog = null)} />
{/if}

<style>
  .creds {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 14px;
    padding: 4px 8px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-2);
    font-size: 12px;
  }
  .pick,
  .actions {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    flex-shrink: 0;
  }
  select {
    max-width: 200px;
  }
  .field {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    min-width: 0;
  }
  .lbl,
  .muted {
    color: var(--text-3);
  }
  .val {
    max-width: 220px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .err {
    color: var(--danger);
  }
  .icon.on {
    color: var(--accent);
  }
  /* Editar, borrar, agregar y cerrar van a la derecha de todo. */
  .actions {
    margin-left: auto;
  }
</style>
