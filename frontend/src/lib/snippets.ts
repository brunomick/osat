import type { Snippet } from './api'
import { isWithin } from './groups'

// Lógica de los snippets que no es dibujar: cuáles se ven en una terminal, cómo
// se buscan y cómo se completan sus variables {{nombre}}.

/**
 * Los snippets que se ven en una terminal. `connGroup` es el grupo de su
 * conexión; null es una terminal de esta PC (solo ve los globales). Un snippet
 * de un grupo se ve en las conexiones de ese grupo y de sus subgrupos.
 */
export function visibleFor(snippets: Snippet[], connGroup: string | null): Snippet[] {
  return snippets.filter((s) => s.group === '' || (connGroup !== null && isWithin(connGroup, s.group)))
}

// Una variable es un nombre (letras, números y _) entre llaves dobles, con espacios opcionales.
// Así no se confunden con lo que ya lleva llaves dobles, como `docker inspect --format '{{.State.Status}}'`.
const VARIABLE = /\{\{\s*([\p{L}_][\p{L}\p{N}_]*)\s*\}\}/gu

/** Los nombres de las variables del comando, sin repetir y en el orden en que aparecen. */
export function variables(command: string): string[] {
  const names: string[] = []
  for (const m of command.matchAll(VARIABLE)) if (!names.includes(m[1])) names.push(m[1])
  return names
}

/** Reemplaza cada variable por su valor, tal cual. Una que no tiene valor queda como estaba. */
export function fill(command: string, values: Record<string, string>): string {
  return command.replace(VARIABLE, (whole, name: string) => (Object.hasOwn(values, name) ? values[name] : whole))
}

/**
 * Filtra por nombre, carpeta y comando, sin distinguir mayúsculas. Con varias
 * palabras, cada una tiene que estar en alguno de los tres. Sin texto, devuelve todos.
 */
export function search(snippets: Snippet[], text: string): Snippet[] {
  const terms = text.toLowerCase().split(/\s+/).filter(Boolean)
  if (!terms.length) return snippets
  return snippets.filter((s) => {
    const hay = `${s.name}\n${s.folder}\n${s.command}`.toLowerCase()
    return terms.every((t) => hay.includes(t))
  })
}

/**
 * Lo que se manda a la terminal: el texto (que se pega, como si se hubiera
 * pegado) y si después va un Enter. Al ejecutar, los saltos de línea del final
 * se sacan para que no se sumen al Enter.
 */
export function toSend(command: string, mode: Snippet['mode']): { text: string; enter: boolean } {
  return mode === 'run' ? { text: command.replace(/(\r?\n)+$/, ''), enter: true } : { text: command, enter: false }
}
