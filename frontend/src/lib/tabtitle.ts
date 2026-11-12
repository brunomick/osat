import type { TabKind } from './stores'
import type { tr } from './i18n'

// Los títulos de pestaña con texto propio de osat no se guardan armados: el título guarda el dato
// (nombre del servidor, archivo) y el texto se arma al mostrarlo, así sigue al idioma elegido.

/** El título para mostrar. `t` es el store `$t` del componente (o `tr` fuera de uno). */
export function tabTitle(t: typeof tr, tab: { kind: TabKind; title: string }): string {
  if (tab.kind === 'local') return t('tab.local')
  if (tab.kind === 'ports') return t('tab.ports', { name: tab.title })
  if (tab.kind === 'browser' && !tab.title) return t('tab.browser')
  if (tab.kind === 'settings') return t('settings.title')
  if (tab.kind === 'import') return t('import.title')
  if (tab.kind === 'snippets') return t('snippets.list')
  if (tab.kind === 'whatsnew') return t('update.newsTitle', { version: tab.title })
  if (tab.kind === 'connection') return tab.title ? `${t('form.editTitle')} · ${tab.title}` : t('form.newTitle')
  return tab.title
}

/** Los workspaces guardados antes del idioma traen el título ya armado en español: se lleva al dato. */
export function legacyTitle(kind: TabKind, title: string): string {
  if (kind === 'local') return ''
  if (kind === 'ports') return title.replace(/^Puertos · /, '')
  if (kind === 'browser') return title === 'Navegador' ? '' : title
  return title
}
