import { derived, get, writable } from 'svelte/store'
import es from './i18n/es.json'
import en from './i18n/en.json'

// No importa stores.ts (stores.ts usa tr()): quien carga o cambia los ajustes hace locale.set().

export type Locale = 'es' | 'en'
export type LanguageSetting = 'system' | Locale
type Dict = { [key: string]: string | Dict }

/** Aplana el diccionario anidado: { a: { b: 'x' } } → { 'a.b': 'x' }. */
export function flatten(d: Dict, prefix = ''): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(d)) {
    if (typeof v === 'string') out[prefix + k] = v
    else Object.assign(out, flatten(v, prefix + k + '.'))
  }
  return out
}

export const dictionaries: Record<Locale, Record<string, string>> = { es: flatten(es), en: flatten(en) }

/** Idioma en uso. Arranca en español hasta que llegan los ajustes. */
export const locale = writable<Locale>('es')

/** "system" elige español si el idioma del sistema lo es, y si no inglés. */
export function resolveLanguage(setting: string, systemLang: string): Locale {
  if (setting === 'es' || setting === 'en') return setting
  return systemLang.toLowerCase().startsWith('es') ? 'es' : 'en'
}

/** La clave existe (en español, que es la que siempre está completa). */
export const hasKey = (key: string) => key in dictionaries.es

function translate(loc: Locale, key: string, vars?: Record<string, string | number>): string {
  let s = dictionaries[loc][key] ?? dictionaries.es[key]
  if (s === undefined) {
    if (import.meta.env?.DEV) console.warn(`i18n: falta la clave "${key}"`)
    return key
  }
  if (vars) s = s.replace(/\{(\w+)\}/g, (m, name) => (name in vars ? String(vars[name]) : m))
  return s
}

/** Para los componentes: `$t('clave', { nombre })`. */
export const t = derived(locale, (loc) => (key: string, vars?: Record<string, string | number>) => translate(loc, key, vars))

/** Para el código fuera de componentes (por ejemplo `toast(tr('clave'))`). */
export function tr(key: string, vars?: Record<string, string | number>): string {
  return translate(get(locale), key, vars)
}

// El atributo lang del documento sigue al idioma.
locale.subscribe((loc) => {
  if (typeof document !== 'undefined') document.documentElement.lang = loc
})
