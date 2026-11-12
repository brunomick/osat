import { describe, expect, it } from 'vitest'
import { isServerGone } from './api'
import { locale, tr } from './i18n'
import { legacyTitle, tabTitle } from './tabtitle'

describe('tabTitle', () => {
  it('arma el título al mostrarlo y sigue al idioma', () => {
    expect(tabTitle(tr, { kind: 'local', title: '' })).toBe('Este equipo')
    expect(tabTitle(tr, { kind: 'ports', title: 'web' })).toBe('Puertos · web')
    expect(tabTitle(tr, { kind: 'browser', title: '' })).toBe('Navegador')
    expect(tabTitle(tr, { kind: 'browser', title: 'index.html' })).toBe('index.html')
    expect(tabTitle(tr, { kind: 'sftp', title: 'web' })).toBe('web')
    expect(tabTitle(tr, { kind: 'settings', title: '' })).toBe(tr('settings.title'))
    expect(tabTitle(tr, { kind: 'import', title: '' })).toBe(tr('import.title'))
    expect(tabTitle(tr, { kind: 'snippets', title: '' })).toBe(tr('snippets.list'))
    expect(tabTitle(tr, { kind: 'whatsnew', title: '0.5.0' })).toBe('Novedades de osat 0.5.0')
    expect(tabTitle(tr, { kind: 'connection', title: '' })).toBe('Nueva conexión')
    expect(tabTitle(tr, { kind: 'connection', title: 'web' })).toBe('Editar conexión · web')
    locale.set('en')
    expect(tabTitle(tr, { kind: 'local', title: '' })).toBe('This computer')
    expect(tabTitle(tr, { kind: 'ports', title: 'web' })).toBe('Ports · web')
    locale.set('es')
  })
  it('los títulos viejos ya armados vuelven a ser el dato', () => {
    expect(legacyTitle('local', 'Este equipo')).toBe('')
    expect(legacyTitle('ports', 'Puertos · web')).toBe('web')
    expect(legacyTitle('ports', 'web')).toBe('web')
    expect(legacyTitle('browser', 'Navegador')).toBe('')
    expect(legacyTitle('sftp', 'Puertos · x')).toBe('Puertos · x')
  })
})

describe('isServerGone', () => {
  it('se reconoce por el código, no por el texto', () => {
    expect(isServerGone(new Error('OSAT:{"code":"SERVER_GONE","message":"otro texto cualquiera"}'))).toBe(true)
    expect(isServerGone('OSAT:{"code":"SERVER_GONE","message":"the connection was lost"}')).toBe(true)
    expect(isServerGone(new Error('se cortó la conexión con el servidor'))).toBe(false)
    expect(isServerGone('OSAT:{"code":"CONFLICT","message":"se cortó la conexión"}')).toBe(false)
  })
})
