import type { Site } from './api'

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
