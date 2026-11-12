// Falla si queda texto visible escrito directamente en un componente o en lib/*.ts en vez de usar el diccionario
// (o si la lista de excepciones tiene algo que ya no se usa). Con VITE_I18N_REPORT=1 imprime lo que encontró.
import { parse } from 'svelte/compiler'
import ts from 'typescript'
import { expect, it } from 'vitest'

// Todos los componentes y módulos de lib/ (sin tests ni los diccionarios).
const svelteFiles = import.meta.glob('../**/*.svelte', { query: '?raw', import: 'default', eager: true }) as Record<string, string>
const tsFiles = import.meta.glob('./*.ts', { query: '?raw', import: 'default', eager: true }) as Record<string, string>

const LETTERS = /\p{L}/u
const ATTRS = new Set(['title', 'placeholder', 'aria-label', 'label', 'alt'])
// Excepciones (nombres propios, siglas, datos técnicos y nombres de teclas): lo que no se traduce.
const EXCEPTIONS = new Set([
  'osat', 'RDP', 'VNC', 'SSH / SFTP', 'Claude MCP', 'Kubernetes', 'Namespace', 'localhost', 'default',
  'Redis', 'RabbitMQ', 'RabbitMQ (web)', 'PostgreSQL', 'MySQL', 'MongoDB', 'Elasticsearch', 'Web',
  'Running', 'ClusterIP', 'NodePort', 'LoadBalancer', 'PuTTY', 'WinSCP', 'OpenSSH', 'Rosé Pine', 'Rosé Pine Dawn',
  'Español', 'English', 'ab', 'Insert', 'Snippets…', 'Broadcast…', 'root', 'Aa', 'v', '-D', '-R',
  // "Acerca de": el lema y el copyright ya estaban en inglés.
  'OPEN SERVER ADMINISTRATION TOOL', 'All your connections in one app.', 'Copyright (C) 2026 Bruno Mick', 'Open source software under GPL-3.0.',
])
// Atajos, nombres de teclas, rutas y comandos de ejemplo.
const EXCEPTION_PATTERNS = [/^(Ctrl|Alt|Shift)\b/, /^(Escape|Esc|Enter|Tab|Home|End|Backspace|Delete|Arrow\w+|Key\w)$/, /^(~|\.|\/)/, /^(curl|cd|EDITOR=|Web\/Nginx|localhost:)/]
const usedExceptions = new Set<string>()
const isException = (s: string) => {
  if (EXCEPTIONS.has(s)) return usedExceptions.add(s) && true
  return EXCEPTION_PATTERNS.some((r) => r.test(s))
}

const found: string[] = []
const report = (file: string, text: string) => {
  const s = text.trim()
  if (s && LETTERS.test(s) && !isException(s)) found.push(`${file}: ${JSON.stringify(s)}`)
  return false
}

// Un literal de código cuenta como texto si tiene letras acentuadas o empieza con mayúscula (descarta clases CSS, 'dark', datos de íconos SVG…).
const looksLikeText = (s: string) =>
  LETTERS.test(s) && !/^[MmLlHhVvCcSsQqTtAaZz\d\s,.-]+$/.test(s) && (/[áéíóúüñÁÉÍÓÚÑ¿¡]/.test(s) || (/^[A-ZÁÉÍÓÚÑ¿¡]/.test(s.trim()) && /[a-z]/.test(s)))

function walk(node: any, file: string) {
  if (!node || typeof node !== 'object') return
  if (Array.isArray(node)) return node.forEach((n) => walk(n, file))
  if (node.type === 'Text') report(file, node.data)
  else if (node.type === 'Attribute') {
    // El valor en texto de un atributo es un dato (class, role, type…) salvo en los que se leen (title, placeholder…).
    if (!Array.isArray(node.value)) return
    for (const v of node.value) if (v.type === 'Text' ? ATTRS.has(node.name) && report(file, v.data) : walk(v, file)) continue
    return
  } else if (node.type === 'Literal' && typeof node.value === 'string' && looksLikeText(node.value)) report(file, node.value)
  else if (node.type === 'TemplateElement' && looksLikeText(node.value?.cooked ?? '')) report(file, node.value.cooked)
  for (const [k, v] of Object.entries(node)) if (k !== 'start' && k !== 'end') walk(v, file)
}

// parse no entiende TypeScript: los <script lang="ts"> se pasan a JavaScript antes.
const stripTypes = (code: string) =>
  code.replace(/<script([^>]*)>([\s\S]*?)<\/script>/g, (_, attrs: string, body: string) => {
    const js = ts.transpileModule(body, { compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext } }).outputText
    return `<script${attrs.replace(/\s*lang=["']ts["']/, '')}>${js}</script>`
  })

it('no queda texto sin traducir escrito en el código', () => {
  for (const [f, code] of Object.entries(svelteFiles)) {
    const ast = parse(stripTypes(code))
    walk(ast.html, f)
    walk(ast.instance?.content, f) // literales de <script>
  }
  // lib/*.ts: literales entre comillas con letras acentuadas (frases en español).
  for (const [f, raw] of Object.entries(tsFiles)) {
    if (f.endsWith('.test.ts')) continue
    const code = raw.replace(/\/\*[\s\S]*?\*\/|\/\/.*$/gm, '')
    for (const m of code.matchAll(/(['"`])((?:\\.|(?!\1).)*[áéíóúüñÁÉÍÓÚÑ¿¡](?:\\.|(?!\1).)*)\1/g)) report(f, m[2])
  }
  if (import.meta.env.VITE_I18N_REPORT) console.log(found.join('\n'))
  expect(found, 'texto sin traducir: pasalo a es.json y en.json').toEqual([])
  expect([...EXCEPTIONS].filter((e) => !usedExceptions.has(e)), 'excepciones que ya no se usan').toEqual([])
})
