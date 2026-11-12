import { describe, expect, it } from 'vitest'
import type { Snippet } from './api'
import { fill, search, toSend, variables, visibleFor } from './snippets'

const sn = (name: string, p: Partial<Snippet> = {}): Snippet => ({
  id: name,
  name,
  folder: '',
  command: 'ls',
  mode: 'paste',
  group: '',
  ...p,
})

describe('visibleFor', () => {
  const all = [
    sn('global'),
    sn('prod', { group: 'Prod' }),
    sn('prod-web', { group: 'Prod/Web' }),
    sn('lab', { group: 'Lab' }),
    sn('prodigio', { group: 'Prodigio' }), // mismo prefijo de texto, otro grupo
  ]
  const names = (g: string | null) => visibleFor(all, g).map((s) => s.name)

  it('una terminal de esta PC solo ve los globales', () => {
    expect(names(null)).toEqual(['global'])
  })
  it('una conexión sin grupo solo ve los globales', () => {
    expect(names('')).toEqual(['global'])
  })
  it('un snippet de grupo se ve en ese grupo y en sus subgrupos', () => {
    expect(names('Prod')).toEqual(['global', 'prod'])
    expect(names('Prod/Web')).toEqual(['global', 'prod', 'prod-web'])
    expect(names('Prod/Web/Nginx')).toEqual(['global', 'prod', 'prod-web'])
  })
  it('no se ve en el grupo padre ni en uno con el mismo prefijo', () => {
    expect(names('Lab')).toEqual(['global', 'lab'])
    expect(names('Prodigio')).toEqual(['global', 'prodigio'])
  })
  it('conserva el orden de entrada', () => {
    expect(visibleFor([all[2], all[0], all[1]], 'Prod/Web').map((s) => s.name)).toEqual(['prod-web', 'global', 'prod'])
  })
})

describe('variables', () => {
  it('nombres únicos en orden de aparición', () => {
    expect(variables('scp {{origen}} {{host}}:{{destino}} && ssh {{host}} ls {{origen}}')).toEqual(['origen', 'host', 'destino'])
  })
  it('sin variables, lista vacía', () => {
    expect(variables('ls -la /var/log')).toEqual([])
    expect(variables('')).toEqual([])
  })
  it('acepta espacios dentro de las llaves, acentos y guion bajo', () => {
    expect(variables('echo {{ nombre }} {{año_2}} {{_x}}')).toEqual(['nombre', 'año_2', '_x'])
  })
  it('funciona con varias líneas', () => {
    expect(variables('cd {{dir}}\ntail -f {{dir}}/{{archivo}}')).toEqual(['dir', 'archivo'])
  })
  it('no toma como variable lo que no es un nombre', () => {
    expect(variables("docker inspect --format '{{.State.Status}}' x")).toEqual([])
    expect(variables('echo {{json .}} {{}} {{ }} {{a-b}} {{1x}} {x} ${{y}}')).toEqual(['y'])
    expect(variables('{{a')).toEqual([])
  })
})

describe('fill', () => {
  it('reemplaza todas las apariciones', () => {
    expect(fill('ssh {{host}} ls; ping {{host}}', { host: 'web1' })).toBe('ssh web1 ls; ping web1')
  })
  it('reemplaza tal cual, sin interpretar nada', () => {
    expect(fill('echo {{x}}', { x: '$& $1 $$ \\n "a" `b` {{y}}' })).toBe('echo $& $1 $$ \\n "a" `b` {{y}}')
  })
  it('un valor vacío vale', () => {
    expect(fill('a{{x}}b', { x: '' })).toBe('ab')
  })
  it('una variable sin valor queda como estaba', () => {
    expect(fill('{{a}} {{b}}', { a: '1' })).toBe('1 {{b}}')
  })
  it('un valor con forma de variable no se vuelve a reemplazar', () => {
    expect(fill('{{a}} {{b}}', { a: '{{b}}', b: 'B' })).toBe('{{b}} B')
  })
  it('reemplaza también con espacios dentro de las llaves', () => {
    expect(fill('echo {{ nombre }}', { nombre: 'hola' })).toBe('echo hola')
  })
  it('no se confunde con nombres como los de Object.prototype', () => {
    expect(fill('{{constructor}} {{toString}}', {})).toBe('{{constructor}} {{toString}}')
  })
  it('conserva los saltos de línea', () => {
    expect(fill('cd {{d}}\nls\n', { d: '/srv' })).toBe('cd /srv\nls\n')
  })
})

describe('search', () => {
  const all = [
    sn('Reiniciar nginx', { folder: 'Web', command: 'sudo systemctl restart nginx' }),
    sn('Espacio en disco', { folder: 'Sistema/Disco', command: 'df -h' }),
    sn('Logs', { folder: 'Web', command: 'tail -f /var/log/nginx/error.log' }),
  ]
  const names = (t: string) => search(all, t).map((s) => s.name)

  it('sin texto devuelve todos', () => {
    expect(names('')).toEqual(['Reiniciar nginx', 'Espacio en disco', 'Logs'])
    expect(names('   ')).toHaveLength(3)
  })
  it('busca en el nombre, la carpeta y el comando', () => {
    expect(names('espacio')).toEqual(['Espacio en disco'])
    expect(names('sistema/disco')).toEqual(['Espacio en disco'])
    expect(names('df -h')).toEqual(['Espacio en disco'])
    expect(names('systemctl')).toEqual(['Reiniciar nginx'])
  })
  it('no distingue mayúsculas', () => {
    expect(names('NGINX')).toEqual(['Reiniciar nginx', 'Logs'])
  })
  it('con varias palabras, todas tienen que estar (aunque sea en campos distintos)', () => {
    expect(names('web nginx')).toEqual(['Reiniciar nginx', 'Logs'])
    expect(names('web restart')).toEqual(['Reiniciar nginx'])
    expect(names('disco nginx')).toEqual([])
  })
  it('sin coincidencias, lista vacía', () => {
    expect(names('zzz')).toEqual([])
  })
})

describe('toSend', () => {
  it('pegar: el comando tal cual y sin Enter', () => {
    expect(toSend('ls -la\n', 'paste')).toEqual({ text: 'ls -la\n', enter: false })
  })
  it('ejecutar: con Enter', () => {
    expect(toSend('ls -la', 'run')).toEqual({ text: 'ls -la', enter: true })
  })
  it('ejecutar: varias líneas tal cual, sin saltos al final', () => {
    expect(toSend('cd /srv\nls\n\n', 'run')).toEqual({ text: 'cd /srv\nls', enter: true })
    expect(toSend('cd /srv\r\nls\r\n', 'run')).toEqual({ text: 'cd /srv\r\nls', enter: true })
  })
})
