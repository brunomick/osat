import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'
import c from 'highlight.js/lib/languages/c'
import cpp from 'highlight.js/lib/languages/cpp'
import csharp from 'highlight.js/lib/languages/csharp'
import css from 'highlight.js/lib/languages/css'
import diff from 'highlight.js/lib/languages/diff'
import dockerfile from 'highlight.js/lib/languages/dockerfile'
import dos from 'highlight.js/lib/languages/dos'
import go from 'highlight.js/lib/languages/go'
import ini from 'highlight.js/lib/languages/ini'
import java from 'highlight.js/lib/languages/java'
import javascript from 'highlight.js/lib/languages/javascript'
import json from 'highlight.js/lib/languages/json'
import lua from 'highlight.js/lib/languages/lua'
import makefile from 'highlight.js/lib/languages/makefile'
import markdown from 'highlight.js/lib/languages/markdown'
import nginx from 'highlight.js/lib/languages/nginx'
import perl from 'highlight.js/lib/languages/perl'
import php from 'highlight.js/lib/languages/php'
import powershell from 'highlight.js/lib/languages/powershell'
import python from 'highlight.js/lib/languages/python'
import ruby from 'highlight.js/lib/languages/ruby'
import rust from 'highlight.js/lib/languages/rust'
import sql from 'highlight.js/lib/languages/sql'
import typescript from 'highlight.js/lib/languages/typescript'
import xml from 'highlight.js/lib/languages/xml'
import yaml from 'highlight.js/lib/languages/yaml'

const languages = { bash, c, cpp, csharp, css, diff, dockerfile, dos, go, ini, java, javascript, json, lua, makefile, markdown, nginx, perl, php, powershell, python, ruby, rust, sql, typescript, xml, yaml }
for (const [name, def] of Object.entries(languages)) hljs.registerLanguage(name, def)

/** Más grande que esto no se colorea: se vuelve a colorear entero en cada tecla. */
export const MAX_HIGHLIGHT = 200 * 1024

const BY_EXT: Record<string, string> = {
  c: 'c',
  h: 'cpp', // los .h pueden ser C o C++: cpp cubre los dos
  cpp: 'cpp', cc: 'cpp', cxx: 'cpp', hpp: 'cpp', hh: 'cpp', hxx: 'cpp', ino: 'cpp',
  cs: 'csharp',
  java: 'java',
  js: 'javascript', mjs: 'javascript', cjs: 'javascript', jsx: 'javascript',
  ts: 'typescript', tsx: 'typescript',
  json: 'json', jsonc: 'json',
  go: 'go',
  py: 'python', pyw: 'python',
  sh: 'bash', bash: 'bash', zsh: 'bash', ksh: 'bash',
  ps1: 'powershell', psm1: 'powershell',
  bat: 'dos', cmd: 'dos',
  sql: 'sql',
  xml: 'xml', html: 'xml', htm: 'xml', svg: 'xml', xaml: 'xml', csproj: 'xml', vcxproj: 'xml', plist: 'xml',
  css: 'css',
  ini: 'ini', conf: 'ini', cfg: 'ini', toml: 'ini', properties: 'ini', service: 'ini', desktop: 'ini',
  yml: 'yaml', yaml: 'yaml',
  md: 'markdown', markdown: 'markdown',
  php: 'php',
  rb: 'ruby',
  rs: 'rust',
  lua: 'lua',
  pl: 'perl', pm: 'perl',
  diff: 'diff', patch: 'diff',
  mk: 'makefile',
}

const BY_NAME: Record<string, string> = {
  makefile: 'makefile',
  gnumakefile: 'makefile',
  dockerfile: 'dockerfile',
  '.bashrc': 'bash',
  '.bash_profile': 'bash',
  '.bash_aliases': 'bash',
  '.profile': 'bash',
  '.zshrc': 'bash',
  'nginx.conf': 'nginx',
}

/** Lenguaje de un archivo según su nombre o extensión, o null si no se conoce. */
export function languageFor(path: string): string | null {
  const name = (path.split(/[\\/]/).pop() ?? '').toLowerCase()
  if (BY_NAME[name]) return BY_NAME[name]
  const i = name.lastIndexOf('.')
  return i > 0 ? (BY_EXT[name.slice(i + 1)] ?? null) : null
}

/**
 * HTML del código con <span class="hljs-…"> en cada pedazo (el texto sale
 * escapado y sin cambios). null si el lenguaje falla: se muestra sin colores.
 */
export function highlightCode(code: string, language: string): string | null {
  try {
    return hljs.highlight(code, { language, ignoreIllegals: true }).value
  } catch {
    return null
  }
}
