/** Lo que se escribió en la barra de direcciones de un sitio, como dirección http(s); null si no sirve.
 * Sin esquema se le agrega https://. No hay buscador: solo direcciones. */
export function normalizeSiteUrl(input: string): string | null {
  let s = input.trim()
  if (!s) return null
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(s)) {
    // "javascript:…", "mailto:…" y similares no son direcciones; "localhost:3000" sí (el ":" va seguido de un puerto).
    if (/^[a-z][a-z0-9+.-]*:(?!\d+(?:[/?#]|$))/i.test(s)) return null
    s = 'https://' + s
  }
  try {
    const u = new URL(s)
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.hostname ? s : null
  } catch {
    return null
  }
}

/** ¿Es la misma dirección? Ignora diferencias que no cambian la página ("https://x.com" y "https://x.com/"). */
export function sameSiteUrl(a: string, b: string): boolean {
  const norm = (s: string) => {
    try {
      return new URL(s).href
    } catch {
      return s
    }
  }
  return norm(a) === norm(b)
}

/** El host de una dirección (para nombrar un sitio sin título); si no se entiende, la dirección tal cual. */
export function siteHost(url: string): string {
  try {
    return new URL(url).hostname || url
  } catch {
    return url
  }
}
