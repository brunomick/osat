<script lang="ts">
  import { t, tr } from '../lib/i18n'
  import { onMount } from 'svelte'
  import { api, clipboardSet, errorText, type DefaultCreds, type Settings } from '../lib/api'
  import { confirmDialog, questionsDialog } from '../lib/dialog'
  import { lockState, refreshLock } from '../lib/lock'
  import { browserSupported, closeTab, defaultShell, loadLocalShells, localShells, patchTab, settings, settingsSection, tabs, toast, updateSettings, type Tab } from '../lib/stores'
  import { appThemes, lightThemes, resolve, termThemeOf, termThemes, themeNames, type TermThemeName, type TermThemeSetting, type UiTheme } from '../lib/termthemes'
  import { uiTheme } from '../lib/theme'
  import Icon from '../lib/Icon.svelte'
  import ThemePicker, { type ThemeChoice } from './ThemePicker.svelte'
  import TermPreview from './TermPreview.svelte'
  import ClaudeSettings from './ClaudeSettings.svelte'
  import { version } from '../../package.json'
  import { isAndroid } from '../lib/platform'
  import { checkForUpdates } from '../lib/updates'

  export let tab: Tab

  // Cada categoría se despliega o se pliega; se recuerda cuáles quedaron abiertas (al principio, solo Apariencia).
  const OPEN_KEY = 'osat.settingsOpen'
  let opened: Record<string, boolean> = (() => {
    try {
      return JSON.parse(localStorage.getItem(OPEN_KEY) ?? '{"appearance":true}') as Record<string, boolean>
    } catch {
      return { appearance: true }
    }
  })()
  function toggle(id: string, e: Event) {
    opened = { ...opened, [id]: (e.currentTarget as HTMLDetailsElement).open }
    try {
      localStorage.setItem(OPEN_KEY, JSON.stringify(opened))
    } catch {
      // sin almacenamiento: vale hasta cerrar la pestaña
    }
  }

  // Compartir una terminal con el puente apagado abre esta pestaña con "Claude MCP" desplegada (ver toggleShare).
  let claudeCat: HTMLDetailsElement
  $: if ($settingsSection === 'claude' && claudeCat) {
    settingsSection.set(null)
    claudeCat.open = true
    claudeCat.scrollIntoView({ block: 'start' })
  }

  // Buscar actualizaciones a mano (botón de General).
  let checking = false
  async function checkNow() {
    checking = true
    await checkForUpdates(true)
    checking = false
  }

  // Cada cambio se guarda al momento (ver updateSettings).
  type Terminal = Settings['terminal']
  type Files = Settings['files']
  const setTerminal = (patch: Partial<Terminal>) => updateSettings((s) => ({ ...s, terminal: { ...s.terminal, ...patch } }))
  const setFiles = (patch: Partial<Files>) => updateSettings((s) => ({ ...s, files: { ...s.files, ...patch } }))
  const setGeneral = (patch: Partial<Settings['general']>) => updateSettings((s) => ({ ...s, general: { ...s.general, ...patch } }))

  // Al apagar el navegador se cierran las pestañas de sitios (closeTab cierra su vista nativa); después Go libera el motor.
  async function setWebBrowser(on: boolean) {
    if (!on) for (const t of $tabs.filter((x) => x.kind === 'site')) await closeTab(t.id)
    setGeneral({ webBrowser: on })
  }

  const picked = (e: Event) => (e.currentTarget as HTMLSelectElement).value
  const setCursor = (e: Event) => setTerminal({ cursor: picked(e) as Terminal['cursor'] })
  const setRightClick = (e: Event) => setTerminal({ rightClick: picked(e) as Terminal['rightClick'] })
  const setBell = (e: Event) => setTerminal({ bell: picked(e) as Terminal['bell'] })
  const setConflict = (e: Event) => setFiles({ onConflict: picked(e) as Files['onConflict'] })
  const setLanguage = (e: Event) => setGeneral({ language: picked(e) as Settings['general']['language'] })
  const setShell = (e: Event) => setGeneral({ defaultShell: picked(e) })

  // Nombre de un tema: los de osat, Solarized y Gruvbox van por el diccionario; los demás son nombres propios.
  const themeKeys: Partial<Record<TermThemeName, string>> = {
    'osat-dark': 'settings.themeOsatDark',
    'osat-light': 'settings.themeOsatLight',
    'solarized-dark': 'settings.themeSolarizedDark',
    'solarized-light': 'settings.themeSolarizedLight',
    'gruvbox-dark': 'settings.themeGruvboxDark',
  }
  const themeName = (tf: typeof tr, n: TermThemeName) => {
    const key = themeKeys[n]
    return key ? tf(key) : (themeNames[n] ?? n)
  }

  // Apariencia y colores de la terminal: tarjetas con una muestra de cada tema (ThemePicker). Elegir una (o pasar
  // con las flechas) la aplica al momento. La terminal lista los mismos temas, por el nombre de su paleta.
  const darkUi = appThemes.filter((a) => !lightThemes.has(a))
  const lightUi = appThemes.filter((a) => lightThemes.has(a))
  const card = (tf: typeof tr, a: UiTheme, id: string): ThemeChoice => ({ id, name: themeName(tf, termThemeOf(a)), palette: termThemes[termThemeOf(a)] })
  $: appearanceGroups = [
    { title: '', choices: [{ id: 'system', name: $t('settings.languageSystem'), palette: null }] },
    { title: $t('settings.themesDark'), choices: darkUi.map((a) => card($t, a, a)) },
    { title: $t('settings.themesLight'), choices: lightUi.map((a) => card($t, a, a)) },
  ]
  $: termGroups = [
    { title: '', choices: [{ id: 'auto', name: $t('settings.themeAuto'), palette: resolve('auto', $uiTheme) }] },
    { title: $t('settings.themesDark'), choices: darkUi.map((a) => card($t, a, termThemeOf(a))) },
    { title: $t('settings.themesLight'), choices: lightUi.map((a) => card($t, a, termThemeOf(a))) },
  ]
  const pickAppearance = (e: CustomEvent<string>) => setGeneral({ appearance: e.detail as Settings['general']['appearance'] })
  const pickTermTheme = (e: CustomEvent<string>) => setTerminal({ theme: e.detail as Terminal['theme'] })
  // La vista previa muestra la paleta de la tarjeta que está bajo el mouse, o la elegida.
  let termHover: string | null = null
  $: preview = resolve((termHover ?? $settings.terminal.theme) as TermThemeSetting, $uiTheme)

  // Los campos de texto y número se guardan al terminar de editar (no en cada tecla: "1" de camino a "18" no vale).
  // Si el valor se corrigió al guardar (fuera de rango), el campo muestra el que quedó.
  type NumKey = 'fontSize' | 'scrollback' | 'lineHeight' | 'letterSpacing' | 'padding' | 'scrollSpeed' | 'keepAlive'
  async function setNumber(e: Event, key: NumKey) {
    const el = e.currentTarget as HTMLInputElement
    const n = Number(el.value)
    await setTerminal({ [key]: key === 'lineHeight' ? n : Math.round(n) } as Partial<Terminal>)
    el.value = String($settings.terminal[key])
  }

  // Desplegables de la terminal: cada uno guarda su clave (los numéricos, como número).
  const setPick = (key: keyof Terminal, num = false) => (e: Event) =>
    setTerminal({ [key]: num ? Number(picked(e)) : picked(e) } as Partial<Terminal>)
  const weights = [100, 200, 300, 400, 500, 600, 700, 800, 900]
  const termTypes: Terminal['termType'][] = ['xterm-256color', 'xterm', 'vt100', 'linux']

  async function setSeparators(e: Event) {
    const el = e.currentTarget as HTMLInputElement
    await setTerminal({ wordSeparators: el.value })
    el.value = $settings.terminal.wordSeparators
  }

  async function setLogDir(e: Event) {
    const el = e.currentTarget as HTMLInputElement
    await setTerminal({ logDir: el.value })
    el.value = $settings.terminal.logDir
  }

  async function pickLogDir() {
    try {
      const dir = await api.pickLogDir()
      if (dir) await setTerminal({ logDir: dir })
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function setFont(e: Event) {
    const el = e.currentTarget as HTMLInputElement
    await setTerminal({ font: el.value })
    el.value = $settings.terminal.font
  }

  async function setDownloadDir(e: Event) {
    const el = e.currentTarget as HTMLInputElement
    await setFiles({ downloadDir: el.value })
    el.value = $settings.files.downloadDir
  }

  async function pickDownloadDir() {
    try {
      const dir = await api.pickDownloadDir()
      if (dir) await setFiles({ downloadDir: dir })
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  // La terminal por defecto es una de las detectadas; si la guardada no está entre ellas, el desplegable pide elegir.
  $: shellKnown = $localShells.some((s) => s.id === $defaultShell)

  async function restoreDefaults() {
    const ok = await confirmDialog({
      title: tr('settings.restoreDefaults'),
      message: tr('settings.restoreDefaultsMsg'),
      okText: tr('settings.restore'),
    })
    if (!ok) return
    try {
      const defaults = await api.settingsDefaults()
      await updateSettings(() => defaults)
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  // Para pedir ayuda: la carpeta de los logs de osat y un resumen (versión, sistema, ajustes) para pegar.
  async function openLogs() {
    try {
      await api.localOpen(await api.logDir())
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function copyDiagnostics() {
    try {
      await clipboardSet(await api.diagnostics())
      toast(tr('settings.diagnosticsCopied'), 'success')
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  // Seguridad (bloqueo con contraseña): el estado se consulta al abrir Ajustes y después de cada cambio.
  const refreshLockState = () => refreshLock().catch((e) => toast(errorText(e), 'error'))
  const secret = (text: string) => ({ text, echo: false })

  // Define o cambia la contraseña (current vacía si el bloqueo todavía no estaba activado).
  async function setPassword(current: string, next: string, confirm: string) {
    if (next !== confirm) return toast(tr('settings.lockMismatch'), 'error')
    try {
      await api.lockSetPassword(current, next)
      toast(tr('settings.lockSaved'), 'success')
    } catch (e) {
      toast(errorText(e), 'error')
    }
    await refreshLockState()
  }

  async function enableLock() {
    const a = await questionsDialog({
      title: tr('settings.lockSetTitle'),
      message: tr('settings.lockSetMsg'),
      questions: [secret(tr('settings.lockNew')), secret(tr('settings.lockConfirm'))],
    })
    if (a) await setPassword('', a[0], a[1])
  }

  async function disableLock() {
    const a = await questionsDialog({
      title: tr('settings.lockOffTitle'),
      message: tr('settings.lockOffMsg'),
      questions: [secret(tr('settings.lockCurrent'))],
    })
    if (!a) return
    try {
      await api.lockDisable(a[0])
    } catch (e) {
      toast(errorText(e), 'error')
    }
    await refreshLockState()
  }

  async function toggleLock(e: Event) {
    const el = e.currentTarget as HTMLInputElement
    await (el.checked ? enableLock() : disableLock())
    el.checked = !!$lockState?.enabled // si se canceló o falló, la casilla vuelve a lo que hay
  }

  async function changePassword() {
    const a = await questionsDialog({
      title: tr('settings.lockChange'),
      message: '',
      questions: [secret(tr('settings.lockCurrent')), secret(tr('settings.lockNew')), secret(tr('settings.lockConfirm'))],
    })
    if (a) await setPassword(a[0], a[1], a[2])
  }

  async function setIdleMinutes(e: Event) {
    const el = e.currentTarget as HTMLInputElement
    await setGeneral({ lockIdleMinutes: Math.round(Number(el.value)) })
    el.value = String($settings.general.lockIdleMinutes)
  }

  async function setUseHello(e: Event) {
    await setGeneral({ lockUseHello: (e.currentTarget as HTMLInputElement).checked })
    await refreshLockState()
  }

  // Credenciales por defecto: la contraseña nunca vuelve del backend; vacía = conservar la guardada.
  let defaults: DefaultCreds = { user: '', domain: '', hasSecret: false }
  let defaultSecret = ''
  let savingDefaults = false
  const loadDefaults = () =>
    api.defaultCredsGet().then((d) => (defaults = { ...d, domain: d.domain ?? '' })).catch((e) => toast(errorText(e), 'error'))

  async function saveDefaults(clearSecret = false) {
    savingDefaults = true
    try {
      defaults = await api.defaultCredsSave({ ...defaults, secret: clearSecret ? '' : defaultSecret, keepSecret: !clearSecret })
      defaults.domain ??= ''
      defaultSecret = ''
      toast(tr('settings.defaultsSaved'), 'success')
    } catch (e) {
      toast(errorText(e), 'error')
    } finally {
      savingDefaults = false
    }
  }

  onMount(() => {
    patchTab(tab.id, { status: 'ready' })
    refreshLockState()
    loadDefaults()
    if (!$localShells.length) loadLocalShells()
  })
</script>

<div class="page">
  <div class="body">
    <h2>{$t('settings.title')}</h2>

    <details class="cat" open={opened['appearance']} on:toggle={(e) => toggle('appearance', e)}>
      <summary><Icon name="chevron" size={14} /><h3>{$t('settings.appearance')}</h3></summary>
      <div class="content">
        <p class="muted small">{$t('settings.themeHint')}</p>
        <ThemePicker
          groups={appearanceGroups}
          value={$settings.general.appearance}
          label={$t('settings.appearance')}
          on:pick={pickAppearance}
        />
      </div>
    </details>

    <details class="cat" open={opened['terminal']} on:toggle={(e) => toggle('terminal', e)}>
      <summary><Icon name="chevron" size={14} /><h3>{$t('settings.terminal')}</h3></summary>
      <div class="content term-layout">
        <div class="term-opts">
          <span class="sub">{$t('settings.colors')}</span>
          <ThemePicker
            groups={termGroups}
            value={$settings.terminal.theme}
            label={$t('settings.colors')}
            on:pick={pickTermTheme}
            on:hover={(e) => (termHover = e.detail)}
          />
          <label class="row">
            <span>{$t('settings.font')}</span>
            <input class="mono" value={$settings.terminal.font} spellcheck="false" on:change={setFont} />
          </label>
          <label class="row">
            <span>{$t('settings.fontSize')}</span>
            <input type="number" min="9" max="28" step="1" value={$settings.terminal.fontSize} on:change={(e) => setNumber(e, 'fontSize')} />
          </label>
          <label class="row">
            <span>{$t('settings.scrollback')}</span>
            <input type="number" min="1000" max="100000" step="1000" value={$settings.terminal.scrollback} on:change={(e) => setNumber(e, 'scrollback')} />
          </label>
          <label class="row">
            <span>{$t('settings.cursor')}</span>
            <select value={$settings.terminal.cursor} on:change={setCursor}>
              <option value="bar">{$t('settings.cursorBar')}</option>
              <option value="block">{$t('settings.cursorBlock')}</option>
              <option value="underline">{$t('settings.cursorUnderline')}</option>
            </select>
          </label>
          <label class="row check">
            <input type="checkbox" checked={$settings.terminal.cursorBlink} on:change={(e) => setTerminal({ cursorBlink: e.currentTarget.checked })} />
            <span>{$t('settings.cursorBlink')}</span>
          </label>
          <label class="row check">
            <input type="checkbox" checked={$settings.terminal.copyOnSelect} on:change={(e) => setTerminal({ copyOnSelect: e.currentTarget.checked })} />
            <span>{$t('settings.copyOnSelect')}</span>
          </label>
          <label class="row">
            <span>{$t('settings.rightClick')}</span>
            <select value={$settings.terminal.rightClick} on:change={setRightClick}>
              <option value="paste">{$t('settings.rightClickPaste')}</option>
              <option value="menu">{$t('settings.rightClickMenu')}</option>
            </select>
          </label>
          <label class="row">
            <span>{$t('settings.bell')}</span>
            <select value={$settings.terminal.bell} on:change={setBell}>
              <option value="none">{$t('settings.bellNone')}</option>
              <option value="visual">{$t('settings.bellVisual')}</option>
              <option value="sound">{$t('settings.bellSound')}</option>
            </select>
          </label>
          <p class="muted small">
            {$t('settings.terminalHelp')}
          </p>

          <span class="sub">{$t('settings.termText')}</span>
          <label class="row">
            <span>{$t('settings.lineHeight')}</span>
            <input type="number" min="1" max="2" step="0.05" value={$settings.terminal.lineHeight} on:change={(e) => setNumber(e, 'lineHeight')} />
          </label>
          <label class="row">
            <span>{$t('settings.letterSpacing')}</span>
            <input type="number" min="0" max="5" step="1" value={$settings.terminal.letterSpacing} on:change={(e) => setNumber(e, 'letterSpacing')} />
          </label>
          <label class="row">
            <span>{$t('settings.fontWeight')}</span>
            <select value={$settings.terminal.fontWeight} on:change={setPick('fontWeight', true)}>
              {#each weights as w}<option value={w}>{w}</option>{/each}
            </select>
          </label>
          <label class="row">
            <span>{$t('settings.fontWeightBold')}</span>
            <select value={$settings.terminal.fontWeightBold} on:change={setPick('fontWeightBold', true)}>
              {#each weights as w}<option value={w}>{w}</option>{/each}
            </select>
          </label>
          <label class="row">
            <span>{$t('settings.padding')}</span>
            <input type="number" min="0" max="40" step="1" value={$settings.terminal.padding} on:change={(e) => setNumber(e, 'padding')} />
          </label>
          <label class="row">
            <span>{$t('settings.minContrast')}</span>
            <select value={$settings.terminal.minContrast} on:change={setPick('minContrast', true)}>
              <option value={1}>{$t('settings.contrastOff')}</option>
              <option value={3}>{$t('settings.contrastLow')}</option>
              <option value={4.5}>{$t('settings.contrastMid')}</option>
              <option value={7}>{$t('settings.contrastHigh')}</option>
            </select>
          </label>
          <label class="row check">
            <input type="checkbox" checked={$settings.terminal.boldBright} on:change={(e) => setTerminal({ boldBright: e.currentTarget.checked })} />
            <span>{$t('settings.boldBright')}</span>
          </label>
          <label class="row">
            <span>{$t('settings.cursorInactive')}</span>
            <select value={$settings.terminal.cursorInactive} on:change={setPick('cursorInactive')}>
              <option value="outline">{$t('settings.cursorOutline')}</option>
              <option value="block">{$t('settings.cursorBlock')}</option>
              <option value="bar">{$t('settings.cursorBar')}</option>
              <option value="underline">{$t('settings.cursorUnderline')}</option>
              <option value="none">{$t('settings.cursorHidden')}</option>
            </select>
          </label>

          <span class="sub">{$t('settings.termBehavior')}</span>
          <label class="row check">
            <input type="checkbox" checked={$settings.terminal.pasteConfirm} on:change={(e) => setTerminal({ pasteConfirm: e.currentTarget.checked })} />
            <span>{$t('settings.pasteConfirm')}</span>
          </label>
          <label class="row">
            <span>{$t('settings.scrollSpeed')}</span>
            <input type="number" min="1" max="10" step="1" value={$settings.terminal.scrollSpeed} on:change={(e) => setNumber(e, 'scrollSpeed')} />
          </label>
          <label class="row check">
            <input type="checkbox" checked={$settings.terminal.scrollOnInput} on:change={(e) => setTerminal({ scrollOnInput: e.currentTarget.checked })} />
            <span>{$t('settings.scrollOnInput')}</span>
          </label>
          <label class="row check">
            <input type="checkbox" checked={$settings.terminal.scrollOnOutput} on:change={(e) => setTerminal({ scrollOnOutput: e.currentTarget.checked })} />
            <span>{$t('settings.scrollOnOutput')}</span>
          </label>
          <label class="row">
            <span>{$t('settings.wordSeparators')}</span>
            <input class="mono" value={$settings.terminal.wordSeparators} spellcheck="false" on:change={setSeparators} />
          </label>
          <label class="row">
            <span>{$t('settings.linkClick')}</span>
            <select value={$settings.terminal.linkClick} on:change={setPick('linkClick')}>
              <option value="click">{$t('settings.linkClickPlain')}</option>
              <option value="ctrl">{$t('settings.linkClickCtrl')}</option>
            </select>
          </label>
          <p class="muted small">{$t('settings.behaviorHelp')}</p>

          <span class="sub">{$t('settings.termConnection')}</span>
          <label class="row">
            <span>{$t('settings.keepAlive')}</span>
            <input type="number" min="5" max="300" step="5" value={$settings.terminal.keepAlive} on:change={(e) => setNumber(e, 'keepAlive')} />
          </label>
          <label class="row">
            <span>{$t('settings.termType')}</span>
            <select value={$settings.terminal.termType} on:change={setPick('termType')}>
              {#each termTypes as tt}<option value={tt}>{tt}</option>{/each}
            </select>
          </label>
          <label class="row check">
            <input type="checkbox" checked={$settings.terminal.sessionLog} on:change={(e) => setTerminal({ sessionLog: e.currentTarget.checked })} />
            <span>{$t('settings.sessionLog')}</span>
          </label>
          <div class="row">
            <span>{$t('settings.logDir')}</span>
            <div class="pick">
              <input class="mono" value={$settings.terminal.logDir} spellcheck="false" on:change={setLogDir} />
              <button on:click={pickLogDir}>{$t('settings.choose')}</button>
            </div>
          </div>
          <p class="muted small">{$t('settings.connectionHelp')}</p>
        </div>
        <div class="preview">
          <TermPreview
            palette={preview}
            font={$settings.terminal.font}
            fontSize={$settings.terminal.fontSize}
            cursor={$settings.terminal.cursor}
            blink={$settings.terminal.cursorBlink}
            lineHeight={$settings.terminal.lineHeight}
            letterSpacing={$settings.terminal.letterSpacing}
            weight={$settings.terminal.fontWeight}
            padding={$settings.terminal.padding}
          />
        </div>
      </div>
    </details>

    <details class="cat" open={opened['files']} on:toggle={(e) => toggle('files', e)}>
      <summary><Icon name="chevron" size={14} /><h3>{$t('settings.files')}</h3></summary>
      <div class="content">
        <label class="row check">
          <input type="checkbox" checked={$settings.files.askDownloadDir} on:change={(e) => setFiles({ askDownloadDir: e.currentTarget.checked })} />
          <span>{$t('settings.askDownload')}</span>
        </label>
        <div class="row">
          <span>{$t('settings.downloadDir')}</span>
          <div class="pick">
            <input class="mono" value={$settings.files.downloadDir} spellcheck="false" on:change={setDownloadDir} />
            <button on:click={pickDownloadDir}>{$t('settings.choose')}</button>
          </div>
        </div>
        <label class="row">
          <span>{$t('settings.onConflict')}</span>
          <select value={$settings.files.onConflict} on:change={setConflict}>
            <option value="ask">{$t('settings.conflictAsk')}</option>
            <option value="overwrite">{$t('settings.conflictOverwrite')}</option>
            <option value="skip">{$t('settings.conflictSkip')}</option>
          </select>
        </label>
        <p class="muted small">
          {$t('settings.filesHelp')}
        </p>
      </div>
    </details>

    <details class="cat" open={opened['general']} on:toggle={(e) => toggle('general', e)}>
      <summary><Icon name="chevron" size={14} /><h3>{$t('settings.general')}</h3></summary>
      <div class="content">
        <label class="row">
          <span>{$t('settings.language')}</span>
          <select value={$settings.general.language} on:change={setLanguage}>
            <option value="system">{$t('settings.languageSystem')}</option>
            <option value="es">Español</option>
            <option value="en">English</option>
          </select>
        </label>
        <label class="row">
          <span>{$t('settings.localShell')}</span>
          <select value={shellKnown ? $defaultShell : ''} disabled={!$localShells.length} on:change={setShell}>
            {#if !$localShells.length}
              <option value="">{$t('settings.noShell')}</option>
            {:else if !shellKnown}
              <option value="" disabled>{$t('settings.pickOne')}</option>
            {/if}
            {#each $localShells as sh (sh.id)}
              <option value={sh.id}>{sh.name}</option>
            {/each}
          </select>
        </label>
        <label class="row check">
          <input type="checkbox" checked={$settings.general.verboseLog} on:change={(e) => setGeneral({ verboseLog: e.currentTarget.checked })} />
          <span>{$t('settings.verboseLog')}</span>
        </label>
        <p class="muted small">
          {$t('settings.verboseHelp')} <i>{$t('settings.verboseHelpWhere')}</i>.
        </p>
        <label class="row check">
          <input type="checkbox" checked={$settings.general.restoreSession} on:change={(e) => setGeneral({ restoreSession: e.currentTarget.checked })} />
          <span>{$t('settings.restoreSession')}</span>
        </label>
        <p class="muted small">{$t('settings.restoreHelp')}</p>
        {#if $browserSupported}
          <label class="row check">
            <input type="checkbox" checked={$settings.general.webBrowser} on:change={(e) => setWebBrowser(e.currentTarget.checked)} />
            <span>{$t('settings.webBrowser')}</span>
          </label>
          <p class="muted small">{$t('settings.webBrowserHelp')}</p>
        {/if}
        {#if !isAndroid()}
          <label class="row check">
            <input type="checkbox" checked={$settings.general.autoUpdate} on:change={(e) => setGeneral({ autoUpdate: e.currentTarget.checked })} />
            <span>{$t('settings.autoUpdate')}</span>
          </label>
          <label class="row check">
            <input type="checkbox" checked={$settings.general.betaUpdates} on:change={(e) => setGeneral({ betaUpdates: e.currentTarget.checked })} />
            <span>{$t('settings.betaUpdates')}</span>
          </label>
          <div class="row">
            <span>{$t('settings.versionLabel', { version })}</span>
            <div class="pick"><button disabled={checking} on:click={checkNow}>{$t('settings.checkUpdates')}</button></div>
          </div>
          <p class="muted small">{$t('settings.autoUpdateHelp')}</p>
          <p class="muted small">{$t('settings.betaUpdatesHelp')}</p>
        {/if}
      </div>
    </details>

    <details class="cat" open={opened['security']} on:toggle={(e) => toggle('security', e)}>
      <summary><Icon name="chevron" size={14} /><h3>{$t('settings.security')}</h3></summary>
      <div class="content">
        <label class="row check">
          <input type="checkbox" checked={!!$lockState?.enabled} disabled={!$lockState} on:change={toggleLock} />
          <span>{$t('settings.lockEnable')}</span>
        </label>
        <div class="row">
          <span>{$t('settings.lockPassword')}</span>
          <div class="pick">
            <button disabled={!$lockState?.enabled} on:click={changePassword}>{$t('settings.lockChange')}</button>
          </div>
        </div>
        <label class="row">
          <span>{$t('settings.lockIdle')}</span>
          <input type="number" min="0" max="1440" step="1" disabled={!$lockState?.enabled} value={$settings.general.lockIdleMinutes} on:change={setIdleMinutes} />
        </label>
        {#if $lockState?.helloAvailable}
          <label class="row check">
            <input type="checkbox" checked={$settings.general.lockUseHello} disabled={!$lockState.enabled} on:change={setUseHello} />
            <span>{$t('settings.lockHello')}</span>
          </label>
        {/if}
        <p class="muted small">{$t('settings.lockHelp')}</p>
      </div>
    </details>

    <details class="cat" open={opened['defaults']} on:toggle={(e) => toggle('defaults', e)}>
      <summary><Icon name="chevron" size={14} /><h3>{$t('settings.defaults')}</h3></summary>
      <form class="content" on:submit|preventDefault={() => saveDefaults()}>
        <label class="row">
          <span>{$t('settings.defaultsUser')}</span>
          <input bind:value={defaults.user} autocomplete="off" spellcheck="false" />
        </label>
        <label class="row">
          <span>{$t('settings.defaultsPassword')}</span>
          <div class="pick">
            <input type="password" bind:value={defaultSecret} autocomplete="new-password" placeholder={defaults.hasSecret ? $t('form.secretSaved') : ''} />
            <button type="button" disabled={!defaults.hasSecret || savingDefaults} on:click={() => saveDefaults(true)}>{$t('settings.defaultsClear')}</button>
          </div>
        </label>
        <label class="row">
          <span>{$t('settings.defaultsDomain')}</span>
          <input bind:value={defaults.domain} autocomplete="off" spellcheck="false" />
        </label>
        <div class="row">
          <span />
          <div class="pick"><button type="submit" class="primary" disabled={savingDefaults}>{$t('settings.defaultsSave')}</button></div>
        </div>
        <p class="muted small">{$t('settings.defaultsHelp')}</p>
      </form>
    </details>

    <details class="cat" bind:this={claudeCat} open={opened['claude']} on:toggle={(e) => toggle('claude', e)}>
      <summary><Icon name="chevron" size={14} /><h3>Claude MCP</h3></summary>
      <div class="content"><ClaudeSettings /></div>
    </details>

    <footer>
      <button class="ghost" on:click={restoreDefaults}>{$t('settings.restoreDefaults')}</button>
      <div class="support">
        <button on:click={openLogs}>{$t('settings.openLogs')}</button>
        <button on:click={copyDiagnostics}>{$t('settings.copyDiagnostics')}</button>
      </div>
    </footer>
  </div>
</div>

<style>
  .page {
    height: 100%;
    overflow: auto;
    background: var(--bg-2);
  }
  /* Todo el ancho de la pestaña: las categorías se estiran y los campos quedan a un ancho cómodo. */
  .body {
    padding: 24px 28px;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  h2 {
    margin: 0 0 4px;
    font-size: 16px;
    font-weight: 600;
  }
  h3 {
    margin: 0;
    font-size: 12px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-2);
  }
  .cat {
    background: var(--bg);
    border: 1px solid var(--border);
  }
  summary {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px 14px;
    cursor: pointer;
    list-style: none;
    color: var(--text-3);
  }
  summary::-webkit-details-marker {
    display: none;
  }
  summary:hover h3,
  .cat[open] h3 {
    color: var(--text);
  }
  summary :global(svg) {
    transition: transform 0.15s;
  }
  .cat[open] summary :global(svg) {
    transform: rotate(90deg);
  }
  .content {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 2px 14px 16px 36px;
  }
  .content p {
    max-width: 760px;
  }
  .row {
    display: grid;
    grid-template-columns: 220px minmax(0, 440px);
    align-items: center;
    gap: 12px;
  }
  .row.check {
    grid-template-columns: auto 1fr;
    gap: 8px;
  }
  .row input[type='number'] {
    width: 110px;
  }
  .row select {
    max-width: 100%;
  }
  .pick {
    display: flex;
    gap: 8px;
  }
  .pick input {
    flex: 1;
    min-width: 0;
  }
  p {
    margin: 0;
    line-height: 1.5;
  }
  .small {
    font-size: 12px;
  }
  footer {
    display: flex;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 8px;
  }
  .support {
    display: flex;
    gap: 8px;
  }
  /* Terminal: las opciones a la izquierda y la vista previa al costado, quieta al bajar. Si no hay lugar, abajo. */
  .content.term-layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(320px, 460px);
    align-items: start;
    gap: 24px;
  }
  .term-opts {
    display: flex;
    flex-direction: column;
    gap: 10px;
    min-width: 0;
  }
  .preview {
    position: sticky;
    top: 12px;
  }
  @media (max-width: 1100px) {
    .content.term-layout {
      grid-template-columns: minmax(0, 1fr);
    }
    .preview {
      position: static;
    }
  }
  .sub {
    color: var(--text-2);
  }
</style>
