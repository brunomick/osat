import { beforeEach, describe, expect, it, vi } from 'vitest'
import { get } from 'svelte/store'
import type { Connection } from './api'
import type { LayoutNode, PaneNode, PlainLayout } from './layout'
import type { Tab } from './stores'
import { mountable, restore, snapshot, type DeferTab, type WorkspaceData } from './workspace'

// workspace.ts solo necesita LOCAL de stores; el resto de stores toca el backend.
vi.mock('./stores', () => ({ LOCAL: 'local' }))

const conn = (id: string): Connection => ({ id, name: id, group: '', host: 'h', port: 22, user: 'u', authType: 'agent', hasSecret: false }) as Connection

const tab = (id: string, p: Partial<Tab>): Tab => ({ id, kind: 'terminal', connId: 'c1', title: id, status: 'ready', ...p })
const pane = (id: string, tabs: string[], active: string | null = tabs[0] ?? null): PaneNode => ({ kind: 'pane', id, tabs, active })

// Todo lo que hay en una sesión de trabajo variada: mosaico de 3 paneles.
const tabs: Tab[] = [
  tab('tab-1', { title: 'web', color: '#e5534b', sessionId: 'secreta-1', shared: true }),
  tab('tab-2', { kind: 'sftp', title: 'web', color: '#e5534b', cwd: '/var/www', sessionId: 'secreta-2' }),
  tab('tab-3', { kind: 'local', connId: 'local', title: 'Este equipo', cwd: '/home/yo' }),
  tab('tab-4', { connId: 'local', title: 'WSL', shell: 'wsl', initialDir: '/home/yo/src' }),
  tab('tab-5', { kind: 'editor', title: 'sin guardar.conf', path: '/etc/sin-guardar.conf', dirty: true }),
  tab('tab-6', { kind: 'editor', title: 'hosts', path: '/etc/hosts', sudo: true }),
  tab('tab-7', { kind: 'browser', title: 'Navegador', url: 'http://localhost:3000' }),
  tab('tab-8', { kind: 'repo', title: 'app', path: '/srv/app' }),
  tab('tab-9', { kind: 'ports', title: 'Puertos · web' }),
  tab('tab-10', { kind: 'rdp', connId: 'c2', title: 'escritorio' }),
  tab('tab-11', { kind: 'sftp', connId: 'c9', title: 'borrada', cwd: '/' }),
  tab('tab-12', { kind: 'local', connId: 'local', title: 'Este equipo', cwd: 'Este equipo' }),
]
const layout: LayoutNode = {
  kind: 'split',
  id: 'split-1',
  dir: 'row',
  ratio: 0.4,
  a: pane('pane-1', ['tab-1', 'tab-2', 'tab-5'], 'tab-2'),
  b: {
    kind: 'split',
    id: 'split-2',
    dir: 'col',
    ratio: 0.6,
    a: pane('pane-2', ['tab-3', 'tab-4', 'tab-6', 'tab-12'], 'tab-4'),
    b: pane('pane-3', ['tab-7', 'tab-8', 'tab-9', 'tab-10', 'tab-11']),
  },
}
const conns = [conn('c1'), conn('c2')] // c9 ya no existe

const roundTrip = (d: WorkspaceData) => JSON.parse(JSON.stringify(d)) as WorkspaceData
const panesOf = (n: PlainLayout | null): string[][] => (!n ? [] : n.kind === 'pane' ? [n.tabs] : [...panesOf(n.a), ...panesOf(n.b)])

describe('snapshot', () => {
  const snap = snapshot(tabs, layout)

  it('guarda cada pestaña con lo necesario para abrirla de nuevo', () => {
    const by = Object.fromEntries(snap.tabs.map((t) => [t.title + t.kind, t]))
    expect(by.webterminal).toMatchObject({ kind: 'terminal', connId: 'c1', color: '#e5534b' })
    expect(by.websftp).toMatchObject({ kind: 'sftp', connId: 'c1', cwd: '/var/www' })
    expect(by.WSLterminal).toMatchObject({ connId: 'local', shell: 'wsl', initialDir: '/home/yo/src' })
    expect(by.hostseditor).toMatchObject({ kind: 'editor', path: '/etc/hosts' })
    expect(by.Navegadorbrowser).toMatchObject({ url: 'http://localhost:3000' })
    expect(by.apprepo).toMatchObject({ path: '/srv/app' })
    expect(by['Puertos · webports']).toMatchObject({ kind: 'ports', connId: 'c1' })
    expect(by.escritoriordp).toMatchObject({ connId: 'c2' })
  })
  it('omite los editores con cambios sin guardar, y anota cuáles para avisarlo al abrir', () => {
    expect(snap.tabs.map((t) => t.title)).not.toContain('sin guardar.conf')
    expect(snap.tabs).toHaveLength(11)
    expect(snap.omitted).toEqual(['sin guardar.conf'])
  })
  it('sin editores omitidos no lleva la lista', () => {
    expect(snapshot([tab('a', {})], pane('p', ['a']))).not.toHaveProperty('omitted')
  })
  it('omite el editor y el repositorio sin ruta', () => {
    const s = snapshot([tab('a', { kind: 'editor' }), tab('b', { kind: 'repo' }), tab('c', {})], pane('p', ['a', 'b', 'c']))
    expect(s.tabs.map((t) => t.title)).toEqual(['c'])
  })
  it('nunca guarda sesiones, contraseñas ni estado de la pestaña', () => {
    const allowed = ['ref', 'kind', 'connId', 'title', 'color', 'shell', 'initialDir', 'cwd', 'path', 'url']
    for (const t of snap.tabs) expect(Object.keys(t).filter((k) => !allowed.includes(k))).toEqual([])
    const json = JSON.stringify(snap)
    for (const s of ['secreta-1', 'secreta-2', 'sessionId', 'fromSession', 'shared', 'dirty', 'status', 'tab-1']) expect(json).not.toContain(s)
  })
  it('el mosaico usa las referencias, con sus proporciones y la pestaña activa de cada panel', () => {
    expect(snap.layout).toMatchObject({ kind: 'split', dir: 'row', ratio: 0.4 })
    expect(panesOf(snap.layout)).toEqual([
      ['t1', 't2'],
      ['t3', 't4', 't5', 't6'],
      ['t7', 't8', 't9', 't10', 't11'],
    ])
    const left = (snap.layout as Extract<PlainLayout, { kind: 'split' }>).a
    expect(left).toMatchObject({ active: 't2' })
    expect(JSON.stringify(snap.layout)).not.toContain('pane-')
  })
  it('si la pestaña activa se omitió, el panel queda con la primera', () => {
    const s = snapshot(tabs, pane('p', ['tab-5', 'tab-6'], 'tab-5'))
    expect(s.layout).toEqual({ kind: 'pane', tabs: ['t1'], active: 't1' })
  })
  it('un panel que se queda sin pestañas desaparece, y su división se colapsa', () => {
    const l: LayoutNode = { kind: 'split', id: 's', dir: 'col', ratio: 0.5, a: pane('p1', ['tab-5']), b: pane('p2', ['tab-6']) }
    expect(snapshot(tabs, l).layout).toEqual({ kind: 'pane', tabs: ['t1'], active: 't1' })
  })
  it('sin pestañas, sin mosaico', () => {
    expect(snapshot([], pane('p', []))).toEqual({ version: 1, tabs: [], layout: null })
  })
  it('una pestaña que está en el mosaico pero no en tabs se ignora', () => {
    expect(snapshot([tab('a', {})], pane('p', ['a', 'fantasma'])).layout).toEqual({ kind: 'pane', tabs: ['t1'], active: 't1' })
  })
})

describe('restore', () => {
  const restored = restore(roundTrip(snapshot(tabs, layout)), conns)!

  it('ida y vuelta: las mismas pestañas, con lo que hace falta para abrirlas', () => {
    expect(restored.tabs.map((t) => [t.ref, t.kind, t.connId, t.title])).toEqual([
      ['t1', 'terminal', 'c1', 'web'],
      ['t2', 'sftp', 'c1', 'web'],
      ['t3', 'local', 'local', ''], // los fixtures traen títulos viejos ya armados: vuelven a ser el dato
      ['t4', 'terminal', 'local', 'WSL'],
      ['t5', 'editor', 'c1', 'hosts'],
      ['t6', 'local', 'local', ''],
      ['t7', 'browser', 'c1', ''],
      ['t8', 'repo', 'c1', 'app'],
      ['t9', 'ports', 'c1', 'web'],
      ['t10', 'rdp', 'c2', 'escritorio'],
    ])
    const t = Object.fromEntries(restored.tabs.map((x) => [x.ref, x]))
    expect(t.t1).toMatchObject({ color: '#e5534b' })
    expect(t.t4).toMatchObject({ shell: 'wsl', initialDir: '/home/yo/src' })
    expect(t.t5).toMatchObject({ path: '/etc/hosts' })
    expect(t.t7).toMatchObject({ url: 'http://localhost:3000' })
    expect(t.t8).toMatchObject({ path: '/srv/app' })
  })
  it('el SFTP y "Este equipo" arrancan en la carpeta en la que estaban', () => {
    const t = Object.fromEntries(restored.tabs.map((x) => [x.ref, x]))
    expect(t.t2.initialDir).toBe('/var/www')
    expect(t.t3.initialDir).toBe('/home/yo')
    expect(t.t6.initialDir).toBe('::drives') // la lista de unidades
  })
  it('las pestañas de una conexión que ya no existe no se abren y se avisa cuáles', () => {
    expect(restored.skipped).toEqual(['borrada'])
    expect(restored.tabs.some((t) => t.connId === 'c9')).toBe(false)
    expect(panesOf(restored.layout).flat()).not.toContain('t11')
  })
  it('el mosaico conserva paneles, orden, activas y proporciones', () => {
    expect(panesOf(restored.layout)).toEqual([
      ['t1', 't2'],
      ['t3', 't4', 't5', 't6'],
      ['t7', 't8', 't9', 't10'],
    ])
    const top = restored.layout as Extract<PlainLayout, { kind: 'split' }>
    expect(top).toMatchObject({ dir: 'row', ratio: 0.4 })
    expect(top.a).toMatchObject({ active: 't2' })
    expect(top.b).toMatchObject({ kind: 'split', dir: 'col', ratio: 0.6 })
  })
  it('guardar lo restaurado da lo mismo (ida y vuelta estable)', () => {
    const again = snapshot(
      restored.tabs.map((t, i) => ({ ...t, id: `n-${i}`, status: 'ready' as const, cwd: t.kind === 'sftp' ? t.initialDir : undefined })),
      // el mosaico de lo restaurado, con los ids nuevos
      (function toNode(n: PlainLayout): LayoutNode {
        const id = (r: string) => `n-${restored.tabs.findIndex((t) => t.ref === r)}`
        return n.kind === 'pane'
          ? pane('x', n.tabs.map(id), n.active ? id(n.active) : null)
          : { kind: 'split', id: 'y', dir: n.dir, ratio: n.ratio, a: toNode(n.a), b: toNode(n.b) }
      })(restored.layout!),
    )
    expect(panesOf(again.layout)).toEqual(panesOf(restored.layout))
    expect(again.tabs.map((t) => t.title)).toEqual(restored.tabs.map((t) => t.title))
  })
  it('si se borran todas las conexiones solo quedan las pestañas de esta PC', () => {
    const r = restore(roundTrip(snapshot(tabs, layout)), [])!
    expect(r.tabs.map((t) => t.ref)).toEqual(['t3', 't4', 't6'])
    expect(panesOf(r.layout)).toEqual([['t3', 't4', 't6']])
    expect(r.skipped).toEqual(['web', 'web', 'hosts', 'Navegador', 'app', 'Puertos · web', 'escritorio', 'borrada'])
  })
  it('si no queda ninguna pestaña, sin mosaico', () => {
    const only = snapshot([tab('a', { connId: 'c9' })], pane('p', ['a']))
    expect(restore(only, conns)).toEqual({ tabs: [], layout: null, skipped: ['a'] })
  })
  it('avisa los editores que no se guardaron por tener cambios sin guardar', () => {
    expect(restored.omitted).toEqual(['sin guardar.conf'])
    expect(restore(roundTrip(snapshot([tab('a', {})], pane('p', ['a']))), conns)).not.toHaveProperty('omitted')
  })
  it('si solo había editores sin guardar, no abre nada pero los avisa', () => {
    const s = snapshot([tab('a', { kind: 'editor', path: '/x', dirty: true })], pane('p', ['a']))
    expect(restore(roundTrip(s), conns)).toEqual({ tabs: [], layout: null, skipped: [], omitted: ['a'] })
  })
  it('una lista de omitidos dañada se ignora', () => {
    const r = restore({ version: 1, tabs: [], layout: null, omitted: ['ok', 5, '', null] }, conns)!
    expect(r.omitted).toEqual(['ok'])
    expect(restore({ version: 1, tabs: [], layout: null, omitted: 'x' }, conns)).not.toHaveProperty('omitted')
  })
  it('un espacio vacío da vacío', () => {
    expect(restore(roundTrip(snapshot([], pane('p', []))), conns)).toEqual({ tabs: [], layout: null, skipped: [] })
  })
})

describe('restore con datos que no sirven', () => {
  it('lo que no es un espacio de esta versión da null', () => {
    for (const bad of [null, undefined, 5, 'x', [], {}, { version: 2, tabs: [], layout: null }, { version: 1 }, { version: 1, tabs: 'x' }]) {
      expect(restore(bad, conns)).toBeNull()
    }
  })
  const base = (over: object) => ({
    version: 1,
    tabs: [{ ref: 'a', kind: 'terminal', connId: 'c1', title: 'a' }],
    layout: { kind: 'pane', tabs: ['a'], active: 'a' },
    ...over,
  })
  it('descarta pestañas mal formadas, repetidas o de un tipo desconocido', () => {
    const r = restore(
      base({
        tabs: [
          { ref: 'a', kind: 'terminal', connId: 'c1', title: 'a' },
          { ref: 'a', kind: 'sftp', connId: 'c1', title: 'repetida' },
          { ref: 'b', kind: 'raro', connId: 'c1', title: 'b' },
          { ref: 'c', kind: 'terminal', title: 'sin conexión' },
          { kind: 'terminal', connId: 'c1', title: 'sin ref' },
          'x',
          null,
        ],
        layout: { kind: 'pane', tabs: ['a', 'b', 'c', 'd'], active: 'b' },
      }),
      conns,
    )!
    expect(r.tabs.map((t) => t.title)).toEqual(['a'])
    expect(r.layout).toEqual({ kind: 'pane', tabs: ['a'], active: 'a' })
    expect(r.skipped).toEqual([])
  })
  it('una pestaña que no está en el mosaico no se abre', () => {
    const r = restore(base({ tabs: [{ ref: 'a', kind: 'terminal', connId: 'c1', title: 'a' }, { ref: 'z', kind: 'sftp', connId: 'c1', title: 'z' }] }), conns)!
    expect(r.tabs.map((t) => t.ref)).toEqual(['a'])
  })
  it('ignora campos de tipo equivocado', () => {
    const r = restore(base({ tabs: [{ ref: 'a', kind: 'terminal', connId: 'c1', title: 'a', color: 5, path: ['x'], shell: '' }] }), conns)!
    expect(r.tabs[0]).toMatchObject({ color: undefined, path: undefined, shell: undefined })
  })
  it('un mosaico dañado da vacío y una mitad dañada deja la otra', () => {
    expect(restore(base({ layout: { kind: 'raro' } }), conns)).toEqual({ tabs: [], layout: null, skipped: [] })
    expect(restore(base({ layout: 'x' }), conns)).toEqual({ tabs: [], layout: null, skipped: [] })
    const r = restore(base({ layout: { kind: 'split', dir: 'row', ratio: 0.5, a: { kind: 'raro' }, b: { kind: 'pane', tabs: ['a'], active: null } } }), conns)!
    expect(r.layout).toEqual({ kind: 'pane', tabs: ['a'], active: 'a' })
  })
  it('un mosaico anidado sin fin no tumba nada', () => {
    let deep: unknown = { kind: 'pane', tabs: ['a'], active: 'a' }
    for (let i = 0; i < 5000; i++) deep = { kind: 'split', dir: 'row', ratio: 0.5, a: deep, b: { kind: 'raro' } }
    expect(restore(base({ layout: deep }), conns)).toEqual({ tabs: [], layout: null, skipped: [] })
  })
  it('una dirección o proporción rara se corrige', () => {
    const r = restore(
      base({
        tabs: [
          { ref: 'a', kind: 'terminal', connId: 'c1', title: 'a' },
          { ref: 'b', kind: 'terminal', connId: 'c1', title: 'b' },
        ],
        layout: { kind: 'split', dir: 'x', ratio: 'mucho', a: { kind: 'pane', tabs: ['a'] }, b: { kind: 'pane', tabs: ['b'] } },
      }),
      conns,
    )!
    expect(r.layout).toMatchObject({ kind: 'split', dir: 'row', ratio: 0.5 })
  })
  it('una pestaña repetida en el mosaico queda solo en su primer lugar', () => {
    const r = restore(
      base({ layout: { kind: 'split', dir: 'row', ratio: 0.5, a: { kind: 'pane', tabs: ['a', 'a'] }, b: { kind: 'pane', tabs: ['a'] } } }),
      conns,
    )!
    expect(r.layout).toEqual({ kind: 'pane', tabs: ['a'], active: 'a' })
  })
})

// Lo restaurado se instala con installLayout sin tocar nada más.
describe('con el mosaico real', () => {
  let m: typeof import('./layout')
  beforeEach(async () => {
    vi.resetModules()
    m = await import('./layout')
  })

  it('snapshot → JSON → restore → installLayout reproduce el mosaico', () => {
    for (const id of ['a', 'b', 'c', 'd']) m.addTab(id)
    m.splitWith('c', m.panes(get(m.layout))[0].id, 'right')
    m.splitWith('d', m.panes(get(m.layout))[1].id, 'bottom')
    m.setRatio((get(m.layout) as Extract<LayoutNode, { kind: 'split' }>).id, 0.25)
    m.activate('a')
    const before = get(m.layout)
    const ts = ['a', 'b', 'c', 'd'].map((id) => tab(id, { title: id.toUpperCase() }))

    const r = restore(roundTrip(snapshot(ts, before)), conns)!
    const idOf = Object.fromEntries(r.tabs.map((t) => [t.ref, `nueva-${t.title}`]))
    m.installLayout(r.layout!, idOf)

    const after = get(m.layout)
    const shape = (n: LayoutNode, name: (id: string) => string): unknown =>
      n.kind === 'pane'
        ? { tabs: n.tabs.map(name), active: n.active && name(n.active) }
        : { dir: n.dir, ratio: n.ratio, a: shape(n.a, name), b: shape(n.b, name) }
    expect(shape(after, (id) => id)).toEqual(shape(before, (id) => `nueva-${id.toUpperCase()}`))
  })
})

describe('mountable', () => {
  const d = (id: string, p: Partial<DeferTab> = {}): DeferTab => ({ id, kind: 'terminal', connId: 'c1', status: 'connecting', deferConnect: true, ...p })
  const live = (id: string, p: Partial<DeferTab> = {}): DeferTab => d(id, { deferConnect: undefined, ...p })
  const vis = (...ids: string[]) => new Set(ids)
  const none = new Set<string>()

  it('solo las diferidas que se ven ahora', () => {
    expect(mountable([d('a'), d('b', { connId: 'c2' }), live('c', { connId: 'c3' })], vis('a', 'c'), none)).toEqual([{ id: 'a' }])
  })
  it('una de un servidor que otra pestaña ya tiene conectado reusa esa conexión', () => {
    const tabs = [live('a', { status: 'ready', sessionId: 's-a' }), d('b', { kind: 'sftp' })]
    expect(mountable(tabs, vis('b'), none)).toEqual([{ id: 'b', fromSession: 's-a' }])
  })
  it('no reusa una pestaña que no está conectada ni la de otro servidor', () => {
    const tabs = [live('a', { status: 'error' }), live('x', { connId: 'c2', status: 'ready', sessionId: 's-x' }), d('b')]
    expect(mountable(tabs, vis('b'), none)).toEqual([{ id: 'b' }])
  })
  it('espera mientras otra pestaña del mismo servidor hace su primera conexión', () => {
    const tabs = [live('a'), d('b')]
    expect(mountable(tabs, vis('b'), vis('a'))).toEqual([])
    // Cuando conecta, la reusa.
    expect(mountable([live('a', { status: 'ready', sessionId: 's-a' }), d('b')], vis('b'), vis('a'))).toEqual([{ id: 'b', fromSession: 's-a' }])
    // Si falla, conecta por su cuenta.
    expect(mountable([live('a', { status: 'error' }), d('b')], vis('b'), vis('a'))).toEqual([{ id: 'b' }])
  })
  it('no espera a una pestaña que está reconectando (no es su primera conexión)', () => {
    expect(mountable([live('a'), d('b')], vis('b'), none)).toEqual([{ id: 'b' }])
  })
  it('de las que se ven juntas del mismo servidor, conecta una sola', () => {
    const tabs = [d('a'), d('b', { kind: 'sftp' }), d('c', { connId: 'c2' })]
    expect(mountable(tabs, vis('a', 'b', 'c'), none)).toEqual([{ id: 'a' }, { id: 'c' }])
  })
  it('las de esta PC y las de escritorio remoto se montan enseguida y no comparten', () => {
    const tabs = [
      live('r0', { kind: 'rdp', connId: 'c2', status: 'ready', sessionId: 'rdp-1' }),
      d('l1', { connId: 'local' }),
      d('l2', { kind: 'local', connId: 'local' }),
      d('r1', { kind: 'rdp', connId: 'c2' }),
    ]
    expect(mountable(tabs, vis('l1', 'l2', 'r1'), none)).toEqual([{ id: 'l1' }, { id: 'l2' }, { id: 'r1' }])
  })
  it('una pestaña de escritorio remoto no es fuente para las demás', () => {
    const tabs = [live('r', { kind: 'rdp', status: 'ready', sessionId: 'rdp-1' }), d('t')]
    expect(mountable(tabs, vis('t'), none)).toEqual([{ id: 't' }])
  })
})

describe('VNC', () => {
  const vnc = tab('tab-v', { kind: 'vnc', connId: 'c2', title: 'escritorio linux', color: '#3fb950', sessionId: 'vnc-1' })
  const one: LayoutNode = pane('pane-v', ['tab-v'])

  it('se guarda y se restaura como las demás pestañas de un servidor', () => {
    const data = roundTrip(snapshot([vnc], one))
    expect(data.tabs).toEqual([{ ref: 't1', kind: 'vnc', connId: 'c2', title: 'escritorio linux', color: '#3fb950' }]) // sin la sesión
    const r = restore(data, conns)!
    expect(r.skipped).toEqual([])
    expect(r.tabs).toMatchObject([{ ref: 't1', kind: 'vnc', connId: 'c2', title: 'escritorio linux', color: '#3fb950' }])
  })
  it('si su conexión ya no existe, se avisa', () => {
    const r = restore(roundTrip(snapshot([vnc], one)), [conn('c1')])!
    expect(r.tabs).toEqual([])
    expect(r.skipped).toEqual(['escritorio linux'])
  })
  it('se monta enseguida y no comparte conexión con las demás pestañas del servidor', () => {
    const d = (id: string, p: Partial<DeferTab> = {}): DeferTab => ({ id, kind: 'vnc', connId: 'c2', status: 'connecting', deferConnect: true, ...p })
    const tabs = [d('v1'), d('v2'), d('t', { kind: 'terminal' })]
    expect(mountable(tabs, new Set(['v1', 'v2', 't']), new Set())).toEqual([{ id: 'v1' }, { id: 'v2' }, { id: 't' }])
    // Tampoco es fuente de conexión para una terminal del mismo servidor.
    const live = d('v', { deferConnect: undefined, status: 'ready', sessionId: 'vnc-1' })
    expect(mountable([live, d('t', { kind: 'terminal' })], new Set(['t']), new Set())).toEqual([{ id: 't' }])
  })
})

describe('sitios web', () => {
  const site = (id: string, p: Partial<Tab> = {}) => tab(id, { kind: 'site', connId: '', title: 'Panel', url: 'https://panel.example.com/inicio', ...p })

  it('se guardan con su dirección y vuelven con ella, aunque no sean de ningún servidor', () => {
    const s = snapshot([site('a'), tab('b', {})], pane('p', ['a', 'b']))
    expect(s.tabs[0]).toMatchObject({ kind: 'site', connId: '', title: 'Panel', url: 'https://panel.example.com/inicio' })
    const r = restore(roundTrip(s), conns)!
    expect(r.skipped).toEqual([])
    expect(r.tabs.map((t) => [t.kind, t.url])).toEqual([
      ['site', 'https://panel.example.com/inicio'],
      ['terminal', undefined],
    ])
  })
  it('sin dirección no se guardan', () => {
    expect(snapshot([site('a', { url: undefined })], pane('p', ['a'])).tabs).toEqual([])
  })
  it('sin dirección guardada no se abren', () => {
    const data = roundTrip(snapshot([site('a'), site('b', { url: 'https://b.example.com' })], pane('p', ['a', 'b'])))
    delete data.tabs[0].url
    expect(restore(data, conns)!.tabs.map((t) => t.url)).toEqual(['https://b.example.com'])
  })
  it('se montan enseguida y no comparten conexión con las demás de connId vacío', () => {
    const d = (id: string, p: Partial<DeferTab> = {}): DeferTab => ({ id, kind: 'site', connId: '', status: 'connecting', deferConnect: true, ...p })
    expect(mountable([d('a'), d('b')], new Set(['a', 'b']), new Set())).toEqual([{ id: 'a' }, { id: 'b' }])
  })
})
