package main

import (
	"log/slog"
	"net/url"
	"path/filepath"
	"sync"

	"osat/internal/browser"
	"osat/internal/i18n"
	"osat/internal/store"
)

// ---------- Sitios web guardados ----------

func (a *App) SitesList() []store.Site { return a.store.Sites() }

// SiteSave crea (id vacío, al final) o actualiza (nombre y URL) un sitio.
func (a *App) SiteSave(st store.Site) (store.Site, error) { return a.store.SaveSite(st) }

func (a *App) SiteDelete(id string) error { return a.store.DeleteSite(id) }

// SitePlace mueve el sitio id antes de beforeID ("" = al final).
func (a *App) SitePlace(id, beforeID string) error { return a.store.PlaceSite(id, beforeID) }

// ---------- Navegador nativo de los sitios (internal/browser) ----------

// browserState guarda lo necesario para crear el motor más tarde (al prender el ajuste sin reiniciar).
// El motor, una vez creado, no se destruye: al apagar el ajuste solo se cierran sus vistas, porque
// crear otro dejaría el hilo del anterior colgado.
type browserState struct {
	mu   sync.Mutex
	on   bool // el ajuste está prendido (y hay motor)
	dir  string
	emit func(event string, data ...interface{})
}

// initBrowser recuerda dónde va el perfil y crea el motor si el ajuste está activo y el sistema lo soporta.
func (a *App) initBrowser(dataDir string, emit func(event string, data ...interface{})) {
	a.bstate.dir, a.bstate.emit = dataDir, emit
	a.setBrowser(a.settings.General.WebBrowser)
}

// setBrowser aplica el ajuste: prendido crea el motor (la primera vez) y apagado cierra todas las vistas.
// Si no se puede crear (por ejemplo, falta el runtime de WebView2), osat sigue sin él.
func (a *App) setBrowser(on bool) {
	b := &a.bstate
	b.mu.Lock()
	defer b.mu.Unlock()
	if !on {
		b.on = false
		if a.browser != nil {
			a.browser.CloseAll()
		}
		return
	}
	if !browser.Supported() {
		return
	}
	if a.browser == nil {
		eng, err := browser.New(filepath.Join(b.dir, "browser-profile"), func(ev browser.Event) {
			b.emit("browser:event", ev)
		})
		if err != nil {
			slog.Warn("no se pudo iniciar el navegador de sitios web", "err", err)
			return
		}
		a.browser = eng
	}
	b.on = true
}

// closeBrowser cierra todas las vistas nativas (al cerrar osat).
func (a *App) closeBrowser() {
	a.bstate.mu.Lock()
	defer a.bstate.mu.Unlock()
	if a.browser != nil {
		a.browser.CloseAll()
	}
}

// engine devuelve el motor, o un error si no hay (ajuste apagado, sistema sin soporte o no arrancó).
func (a *App) engine() (browser.Engine, error) {
	a.bstate.mu.Lock()
	defer a.bstate.mu.Unlock()
	if !a.bstate.on {
		return nil, i18n.Err("app.theWebBrowserIsNot")
	}
	return a.browser, nil
}

// checkSiteURL acepta solo http(s) con host.
func checkSiteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return i18n.Err("app.onlyHttpOrHttpsAddresses")
	}
	return nil
}

// BrowserSupported dice si este sistema tiene navegador nativo para los sitios web.
func (a *App) BrowserSupported() bool { return browser.Supported() }

// BrowserOpen crea la vista nativa de la pestaña id y carga rawURL. b es el rectángulo de la
// pestaña en píxeles CSS (dentro del área cliente de la ventana) y scale el devicePixelRatio.
func (a *App) BrowserOpen(id, rawURL string, b browser.Bounds, scale float64) error {
	if err := checkSiteURL(rawURL); err != nil {
		return err
	}
	eng, err := a.engine()
	if err != nil {
		return err
	}
	return eng.Open(id, rawURL, b, scale)
}

func (a *App) BrowserNavigate(id, rawURL string) error {
	if err := checkSiteURL(rawURL); err != nil {
		return err
	}
	eng, err := a.engine()
	if err != nil {
		return err
	}
	return eng.Navigate(id, rawURL)
}

func (a *App) BrowserBack(id string) error {
	eng, err := a.engine()
	if err != nil {
		return err
	}
	return eng.Back(id)
}

func (a *App) BrowserForward(id string) error {
	eng, err := a.engine()
	if err != nil {
		return err
	}
	return eng.Forward(id)
}

func (a *App) BrowserReload(id string) error {
	eng, err := a.engine()
	if err != nil {
		return err
	}
	return eng.Reload(id)
}

func (a *App) BrowserSetBounds(id string, b browser.Bounds, scale float64) error {
	eng, err := a.engine()
	if err != nil {
		return err
	}
	return eng.SetBounds(id, b, scale)
}

// BrowserSetVisible muestra u oculta la vista (se oculta cuando algo de osat tiene que quedar encima).
func (a *App) BrowserSetVisible(id string, v bool) error {
	eng, err := a.engine()
	if err != nil {
		return err
	}
	return eng.SetVisible(id, v)
}

func (a *App) BrowserClose(id string) {
	if eng, err := a.engine(); err == nil {
		eng.Close(id)
	}
}
