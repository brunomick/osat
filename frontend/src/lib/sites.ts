import type { Cred, Site } from './api'
import { sameSiteUrl, siteHost } from './siteUrl'

/** Los sitios que coinciden con la búsqueda (nombre o dirección), en el orden elegido por el usuario. */
export function filterSites(sites: Site[], query: string): Site[] {
  const q = query.trim().toLowerCase()
  return sites
    .filter((s) => !q || s.name.toLowerCase().includes(q) || s.url.toLowerCase().includes(q))
    .sort((a, b) => a.order - b.order)
}

/**
 * Para SitePlace: el id del sitio antes del cual queda `dragId` al soltarlo sobre `ref` (arriba o abajo de esa fila).
 * "" = al final (sin `ref`, o `ref` es la última fila). `shown` es la lista como se ve.
 */
export function placeBeforeId(shown: Site[], dragId: string, ref: string | undefined, side: 'before' | 'after' | undefined): string {
  if (!ref) return ''
  if (side === 'before') return ref
  const i = shown.findIndex((s) => s.id === ref)
  return shown.slice(i + 1).find((s) => s.id !== dragId)?.id ?? ''
}

/**
 * El sitio guardado de una pestaña: el que la abrió (siteId), aunque la página haya navegado (por ejemplo,
 * tras el login); si no hay o ya no existe, el que tiene la misma dirección que la página.
 */
export function siteForTab(sites: Site[], siteId: string | undefined, url: string): Site | undefined {
  return (siteId ? sites.find((s) => s.id === siteId) : undefined) ?? sites.find((s) => sameSiteUrl(s.url, url))
}

/** ¿La credencial es para esta dirección? Su dominio es el host de la página o uno de sus padres (subdominios). */
export function credMatches(c: Cred, url: string): boolean {
  const d = c.domain
  if (!d) return false
  const host = siteHost(url).toLowerCase()
  return host === d || host.endsWith('.' + d)
}
