import { describe, expect, it } from 'vitest'
import type { GitStatus } from './api'
import { changeCount, discardSets, gitLists, letterClass, relTime, repoName, treeMarks } from './git'

const status = (files: GitStatus['files'], root = '/srv/infra'): GitStatus => ({
  isRepo: true,
  root,
  branch: 'main',
  upstream: '',
  ahead: 0,
  behind: 0,
  remote: '',
  host: '',
  files,
  head: '',
  subject: '',
  time: 0,
})

describe('gitLists', () => {
  it('separa preparados y cambios como VS Code', () => {
    const { staged, changes } = gitLists([
      { path: 'b.yml', x: '.', y: 'M' },
      { path: 'a.yml', x: 'M', y: 'M' },
      { path: 'nuevo.txt', x: '?', y: '?' },
      { path: 'viejo.conf', x: 'D', y: '.' },
      { path: 'c.go', x: 'U', y: 'U' },
    ])
    expect(staged.map((i) => `${i.letter} ${i.file.path}`)).toEqual(['M a.yml', 'D viejo.conf'])
    expect(changes.map((i) => `${i.letter} ${i.file.path}`)).toEqual(['M a.yml', 'M b.yml', 'U c.go', 'U nuevo.txt'])
  })

  it('colores de las letras', () => {
    const { staged, changes } = gitLists([
      { path: 'n', x: '?', y: '?' },
      { path: 'd', x: 'D', y: '.' },
      { path: 'm', x: '.', y: 'M' },
      { path: 'u', x: 'U', y: 'U' },
    ])
    expect([...staged, ...changes].map(letterClass)).toEqual(['deleted', 'modified', 'added', 'conflict'])
  })
})

describe('treeMarks', () => {
  it('marca archivos y sus carpetas hasta la raíz del repositorio', () => {
    const m = treeMarks(
      status([
        { path: 'k8s/app/deployment.yaml', x: '.', y: 'M' },
        { path: 'k8s/nuevo.yaml', x: '?', y: '?' },
      ]),
    )
    expect(m.get('/srv/infra/k8s/app/deployment.yaml')).toEqual({ letter: 'M', cls: 'modified' })
    expect(m.get('/srv/infra/k8s/nuevo.yaml')).toEqual({ letter: 'U', cls: 'added' })
    expect(m.get('/srv/infra/k8s')).toEqual({ letter: '', cls: 'modified' })
    expect(m.get('/srv/infra/k8s/app')).toEqual({ letter: '', cls: 'modified' })
    expect(m.get('/srv/infra')).toEqual({ letter: '', cls: 'modified' })
    expect(m.has('/srv')).toBe(false)
  })

  it('repositorio en la raíz del disco', () => {
    const m = treeMarks(status([{ path: 'etc/x.conf', x: '.', y: 'M' }], '/'))
    expect(m.get('/etc/x.conf')?.letter).toBe('M')
    expect(m.get('/etc')?.cls).toBe('modified')
    expect(m.get('/')?.cls).toBe('modified')
  })

  it('sin repositorio no hay marcas', () => {
    expect(treeMarks(null).size).toBe(0)
    expect(changeCount(null)).toBe(0)
  })
})

describe('repoName', () => {
  it.each([
    ['https://github.com/acme/infra.git', 'acme/infra'],
    ['git@github.com:acme/infra.git', 'acme/infra'],
    ['ssh://git@gitlab.local:2222/ops/deploy', 'ops/deploy'],
    ['/srv/git/infra.git', 'git/infra'],
  ])('%s', (url, want) => expect(repoName(url)).toBe(want))
})

it('relTime en el idioma de osat', () => {
  const now = 1_800_000_000_000
  expect(relTime(now / 1000 - 300, 'es', now)).toBe('hace 5 minutos')
  expect(relTime(now / 1000 - 7200, 'en', now)).toBe('2 hours ago')
  expect(relTime(now / 1000 - 86400, 'es', now)).toBe('ayer')
})

it('discardSets separa los archivos nuevos', () => {
  const { changes } = gitLists([
    { path: 'a', x: '.', y: 'M' },
    { path: 'b', x: '?', y: '?' },
  ])
  expect(discardSets(changes)).toEqual({ tracked: ['a'], untracked: ['b'] })
})
