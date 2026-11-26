<script lang="ts">
  import { t, tr } from '../lib/i18n'
  import { createEventDispatcher } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { api, clipboardSet, errorText, type Cred, type CredData } from '../lib/api'
  import { isCredsLost } from '../lib/credsvault'
  import { confirmDialog } from '../lib/dialog'
  import { copySecret } from '../lib/secretclip'
  import { credMatches } from '../lib/sites'
  import { siteHost } from '../lib/siteUrl'
  import { creds, reloadCreds, toast } from '../lib/stores'
  import CredDialog from './CredDialog.svelte'

  // Panel lateral de credenciales: lo abre la llave de la barra o, solo, SiteView al cargar un login de un dominio con
  // credenciales. Va al costado de la
  // página y le quita ancho (la vista nativa se dibuja encima de todo: no puede flotar sobre ella sin taparla).
  // No toma el foco: la persona puede estar escribiendo en la página. Muestra todas las credenciales: primero las del
  // dominio de la página (las únicas que se pueden rellenar ahí) y después las otras, para copiar. Desde acá se
  // agregan, editan y borran.

  /** La pestaña del sitio y la dirección de su página. */
  export let tabId: string
  export let url: string

  const dispatch = createEventDispatcher<{ dismiss: void; filled: void }>()

  let busy = ''
  let error = ''
  /** Los datos de cada credencial (del almacén del sistema, al mostrarse el panel) y si su contraseña está a la vista. */
  let data: Record<string, CredData> = {}
  let shown: Record<string, boolean> = {}
  /** Las que no se pueden descifrar en esta PC (se cargan de nuevo editándolas). */
  let lost: Record<string, boolean> = {}
  let seq = 0
  /** El diálogo abierto: { cred } edita, { cred: undefined } crea; null = cerrado. */
  let dialog: { cred: Cred | undefined } | null = null

  $: sorted = [...$creds].sort((a, b) => a.label.localeCompare(b.label, undefined, { sensitivity: 'base' }))
  $: groups = [
    { key: 'page', title: $t('site.credsForPage'), list: sorted.filter((c) => credMatches(c, url)), canFill: true },
    { key: 'other', title: $t('site.credsOther'), list: sorted.filter((c) => !credMatches(c, url)), canFill: false },
  ].filter((g) => g.list.length)

  // sorted cambia con $creds: si se edita alguna, se vuelve a leer.
  $: load(sorted)

  async function load(list: Cred[]) {
    const my = ++seq
    const next: Record<string, CredData> = {}
    const nextLost: Record<string, boolean> = {}
    for (const c of list) {
      try {
        next[c.id] = await api.credGet(c.id)
      } catch (e) {
        // sin datos (por ejemplo, osat se bloqueó): la tarjeta queda solo con Rellenar
        if (isCredsLost(e)) nextLost[c.id] = true
      }
    }
    if (my === seq) (data = next), (lost = nextLost)
  }

  function fields(d: CredData | undefined) {
    if (!d) return []
    return [
      { key: 'account', label: tr('site.credAccount'), value: d.account, secret: false },
      { key: 'user', label: tr('site.credUser'), value: d.user, secret: false },
      { key: 'password', label: tr('site.credPassword'), value: d.password, secret: true },
    ].filter((f) => f.value) // los que no se cargaron no se muestran
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

  async function fill(c: Cred) {
    if (busy) return
    busy = c.id
    error = ''
    try {
      await api.browserFill(tabId, c.id)
      toast(tr('site.credFilled'), 'success')
      dispatch('filled')
    } catch (e) {
      error = errorText(e)
    } finally {
      busy = ''
    }
  }
</script>

<aside class="panel" aria-label={$t('site.creds')}>
  <header>
    <Icon name="key" size={14} />
    <span class="title">{$t('site.creds')}</span>
    <button type="button" class="icon" title={$t('site.credNew')} aria-label={$t('site.credNew')} on:click={() => (dialog = { cred: undefined })}>
      <Icon name="plus" size={14} />
    </button>
    <button type="button" class="icon" title={$t('site.credsNotNow')} aria-label={$t('site.credsNotNow')} on:click={() => dispatch('dismiss')}>
      <Icon name="x" size={14} />
    </button>
  </header>
  {#if !groups.length}
    <p class="muted">{$t('site.credsEmpty')}</p>
  {/if}
  {#each groups as g (g.key)}
    <h3>{g.title}</h3>
    <ul>
      {#each g.list as c (c.id)}
        <li>
          <span class="head">
            <span class="label" title={c.label}>{c.label}</span>
            <button type="button" class="icon" title={$t('site.credEdit')} aria-label={$t('site.credEdit')} on:click={() => (dialog = { cred: c })}>
              <Icon name="edit" size={14} />
            </button>
            <button type="button" class="icon" title={$t('site.credDelete')} aria-label={$t('site.credDelete')} on:click={() => remove(c)}>
              <Icon name="trash" size={14} />
            </button>
          </span>
          <span class="domain mono" title={c.domain}>{c.domain}</span>
          {#if lost[c.id]}<span class="lost">{$t('site.credLostNote')}</span>{/if}
          {#each fields(data[c.id]) as f (f.key)}
            <span class="field">
              <span class="lbl">{f.label}</span>
              {#if f.secret && !shown[c.id]}
                <span class="val mono">••••••••</span>
              {:else}
                <span class="val mono" title={f.value}>{f.value}</span>
              {/if}
              {#if f.secret}
                <button
                  type="button"
                  class="icon"
                  class:on={shown[c.id]}
                  title={shown[c.id] ? $t('site.hide') : $t('site.show')}
                  aria-label={shown[c.id] ? $t('site.hide') : $t('site.show')}
                  aria-pressed={!!shown[c.id]}
                  on:click={() => (shown = { ...shown, [c.id]: !shown[c.id] })}
                >
                  <Icon name="eye" size={14} />
                </button>
              {/if}
              <button type="button" class="icon" title={$t('site.copy') + ': ' + f.label} aria-label={$t('site.copy') + ': ' + f.label} on:click={() => copy(f.label, f.value, f.secret)}>
                <Icon name="copy" size={14} />
              </button>
            </span>
          {/each}
          {#if g.canFill}
            <button type="button" class="primary" disabled={!!busy} on:click={() => fill(c)}>
              {busy === c.id ? '…' : $t('site.credFill')}
            </button>
          {/if}
        </li>
      {/each}
    </ul>
  {/each}
  {#if error}<p class="err">{error}</p>{/if}
  <footer>
    <button type="button" on:click={() => dispatch('dismiss')}>{$t('site.credsNotNow')}</button>
  </footer>
</aside>

{#if dialog}
  <CredDialog cred={dialog.cred} domain={siteHost(url)} on:close={() => (dialog = null)} />
{/if}

<style>
  .panel {
    width: 240px;
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 10px;
    border-left: 1px solid var(--border);
    background: var(--bg-2);
    font-size: 12px;
    overflow: auto;
  }
  header {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .title {
    flex: 1;
    font-weight: 600;
  }
  h3 {
    margin: 4px 0 0;
    font-size: 11px;
    font-weight: 600;
    color: var(--text-3);
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  li {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 8px;
    border: 1px solid var(--border);
    background: var(--bg);
  }
  .label,
  .domain {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 2px;
    min-width: 0;
  }
  .label {
    flex: 1;
    min-width: 0;
    font-weight: 600;
  }
  .lost {
    color: var(--warning);
    font-size: 11px;
  }
  .muted {
    margin: 0;
    color: var(--text-3);
  }
  .domain {
    color: var(--text-3);
    font-size: 11px;
  }
  .field {
    display: flex;
    align-items: center;
    gap: 4px;
    min-width: 0;
  }
  .lbl {
    color: var(--text-3);
    flex-shrink: 0;
  }
  .val {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .icon.on {
    color: var(--accent);
  }
  li button.primary {
    align-self: flex-start;
    padding: 2px 12px;
    font-size: 12px;
  }
  .err {
    margin: 0;
    color: var(--danger);
  }
  footer {
    display: flex;
    justify-content: flex-end;
  }
  footer button {
    padding: 2px 10px;
    font-size: 12px;
  }
</style>
