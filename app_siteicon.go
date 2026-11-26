package main

// Íconos de los sitios guardados: los muestra la barra lateral. Al cargar la página de un sitio, osat le pregunta
// qué ícono declara (o usa /favicon.ico) y lo baja desde Go, sin cookies y sin servicios de terceros. Se guarda
// en sites.json como data: URL, así la barra lo muestra sin abrir la página.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"osat/internal/store"
)

const (
	siteIconMaxBytes = 64 << 10           // un ícono más grande no se guarda
	siteIconFresh    = 7 * 24 * time.Hour // un ícono bajado se renueva una vez por semana
	siteIconRetry    = 24 * time.Hour     // si no se pudo bajar, se reintenta al día siguiente
)

// siteIconJS corre en la página: su host y los íconos que declara, en orden de preferencia (los de
// <link rel="icon"> primero, el de Apple después y /favicon.ico al final). Las direcciones ya vienen absolutas.
const siteIconJS = `(function () {
  var out = []
  document.querySelectorAll('link[rel~="icon"]').forEach(function (l) { if (l.href) out.push(l.href) })
  document.querySelectorAll('link[rel="apple-touch-icon"]').forEach(function (l) { if (l.href) out.push(l.href) })
  out.push(location.origin + '/favicon.ico')
  return { host: location.hostname, urls: out }
})()`

var siteIconClient = &http.Client{Timeout: 10 * time.Second}

// SiteIconRefresh busca el ícono del sitio guardado siteID en la página de la pestaña tabID, si hace falta
// (no tiene, o es de hace más de una semana). Devuelve si cambió. La página tiene que estar en el host del
// sitio: si navegó a otro (el login de un proveedor, por ejemplo), no hace nada.
func (a *App) SiteIconRefresh(tabID, siteID string) (bool, error) {
	var site store.Site
	for _, st := range a.store.Sites() {
		if st.ID == siteID {
			site = st
		}
	}
	if site.ID == "" {
		return false, nil
	}
	now := time.Now()
	if age := now.Sub(time.Unix(site.IconAt, 0)); site.IconAt != 0 && (age < siteIconRetry || (site.Icon != "" && age < siteIconFresh)) {
		return false, nil
	}
	eng, err := a.engine()
	if err != nil {
		return false, err
	}
	out, err := eng.Eval(tabID, siteIconJS)
	if err != nil {
		return false, err
	}
	var page struct {
		Host string   `json:"host"`
		URLs []string `json:"urls"`
	}
	if json.Unmarshal([]byte(out), &page) != nil {
		return false, nil
	}
	if u, err := url.Parse(site.URL); err != nil || !strings.EqualFold(u.Hostname(), page.Host) {
		return false, nil
	}
	icon := fetchSiteIcon(siteIconClient, page.URLs)
	if icon == "" {
		icon = site.Icon // si esta vez no se pudo, queda el que había
	}
	return icon != site.Icon, a.store.SetSiteIcon(siteID, icon, now.Unix())
}

// fetchSiteIcon baja el primero de urls que sea una imagen chica y la devuelve como data: URL ("" si ninguno).
func fetchSiteIcon(c *http.Client, urls []string) string {
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		if icon := fetchOneIcon(c, u.String()); icon != "" {
			return icon
		}
	}
	return ""
}

func fetchOneIcon(c *http.Client, u string) string {
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ""
	}
	resp, err := c.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, siteIconMaxBytes+1))
	if err != nil || len(b) == 0 || len(b) > siteIconMaxBytes {
		return ""
	}
	// El tipo sale del contenido, no de lo que diga el servidor (una página de error puede venir con 200).
	kind := http.DetectContentType(b)
	if !strings.HasPrefix(kind, "image/") {
		declared, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if declared != "image/svg+xml" && !bytes.Contains(b[:min(len(b), 512)], []byte("<svg")) {
			return ""
		}
		kind = "image/svg+xml"
	}
	return "data:" + kind + ";base64," + base64.StdEncoding.EncodeToString(b)
}
