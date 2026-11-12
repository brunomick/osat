// Markdown mínimo para las notas de versión (CHANGELOG y Releases): títulos ###, listas con "-",
// párrafos, **negrita**, *cursiva*, `código` y [enlaces](url), que se muestran como texto.
// Todo se escapa antes: el resultado es seguro para {@html}.

const escape = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

function inline(s: string): string {
  const parts = escape(s).split(/(`[^`]+`)/g) // el código se deja tal cual
  return parts
    .map((p) =>
      p.startsWith('`') && p.endsWith('`') && p.length > 1
        ? `<code>${p.slice(1, -1)}</code>`
        : p
            .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
            .replace(/\*\*([^*]+)\*\*/g, '<b>$1</b>')
            .replace(/(^|[^*\w])\*([^*\s][^*]*)\*/g, '$1<i>$2</i>'),
    )
    .join('')
}

export function renderMarkdown(md: string): string {
  const out: string[] = []
  let list: string[] = []
  let para: string[] = []
  const flush = () => {
    if (para.length) out.push(`<p>${inline(para.join(' '))}</p>`)
    if (list.length) out.push(`<ul>${list.map((li) => `<li>${inline(li)}</li>`).join('')}</ul>`)
    para = []
    list = []
  }
  for (const raw of md.replace(/\r\n/g, '\n').split('\n')) {
    const line = raw.trimEnd()
    const h = /^(#{1,6})\s+(.*)$/.exec(line)
    const li = /^\s*[-*]\s+(.*)$/.exec(line)
    if (!line.trim()) flush()
    else if (h) {
      flush()
      out.push(`<h4>${inline(h[2])}</h4>`)
    } else if (li) {
      if (para.length) flush()
      list.push(li[1])
    } else if (list.length && /^\s+/.test(raw)) list[list.length - 1] += ' ' + line.trim() // continuación del ítem
    else {
      if (list.length) flush()
      para.push(line.trim())
    }
  }
  flush()
  return out.join('')
}
