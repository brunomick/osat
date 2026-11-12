package web

import (
	"context"
	"crypto/tls"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"osat/internal/i18n"
)

// Dial abre una conexión al destino (p. ej. a través de la conexión SSH).
type Dial func(ctx context.Context, network, addr string) (net.Conn, error)

// Target es el destino al que apunta un proxy.
type Target struct {
	Scheme string // "http" (por defecto) o "https"
	Host   string // "localhost", "127.0.0.1"...
	Port   int
}

func (t Target) hostPort() string { return net.JoinHostPort(t.Host, strconv.Itoa(t.Port)) }

// IsLoopback dice si host es esta misma máquina (localhost, 127.x, ::1).
func IsLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Valid dice si el destino se puede usar.
func (t Target) Valid() error {
	if t.Port < 1 || t.Port > 65535 {
		return errBadPort
	}
	if t.Host == "" || strings.ContainsAny(t.Host, "/@ ") {
		return i18n.Err("app.invalidHostQ", t.Host)
	}
	if t.Scheme != "" && t.Scheme != "http" && t.Scheme != "https" {
		return i18n.Err("app.invalidSchemeQ", t.Scheme)
	}
	return nil
}

var frameAncestors = regexp.MustCompile(`(?i)(^|;)\s*frame-ancestors[^;]*`)

// Proxy hace de intermediario hacia t, abriendo las conexiones con dial. El
// navegador integrado muestra los sitios en un iframe, así que se sacan las
// cabeceras con las que un sitio prohíbe que lo muestren dentro de otro; y el
// destino ve su propio nombre ("localhost:3000") como Host y Origin, porque
// muchos servidores de desarrollo rechazan cualquier otro. También sirve para
// websockets (recarga en caliente).
func Proxy(t Target, dial Dial) http.Handler {
	scheme := t.Scheme
	if scheme == "" {
		scheme = "http"
	}
	target := &url.URL{Scheme: scheme, Host: t.hostPort()}
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme = target.Scheme
			r.Out.URL.Host = target.Host
			r.Out.Host = target.Host
			if r.Out.Header.Get("Origin") != "" {
				r.Out.Header.Set("Origin", target.Scheme+"://"+target.Host)
			}
			if r.Out.Header.Get("Referer") != "" {
				r.Out.Header.Set("Referer", target.Scheme+"://"+target.Host+"/")
			}
		},
		Transport: &http.Transport{
			DialContext: dial,
			// Un https en el propio localhost casi siempre tiene un certificado propio.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: IsLoopback(t.Host)}, //nolint:gosec // solo destinos de loopback
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("X-Frame-Options")
			if csp := resp.Header.Get("Content-Security-Policy"); csp != "" {
				resp.Header.Set("Content-Security-Policy", strings.TrimPrefix(strings.TrimSpace(frameAncestors.ReplaceAllString(csp, "$1")), ";"))
			}
			// Una redirección al nombre del destino sacaría la página del proxy.
			if loc, err := resp.Location(); err == nil && loc.Host == target.Host {
				loc.Scheme, loc.Host = "", ""
				resp.Header.Set("Location", loc.String())
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><body style="font:14px sans-serif;margin:24px;color:#444">`+
				`<h3>%s</h3><p>%s</p><p>%s</p>`,
				html.EscapeString(i18n.T("web.cantOpen", target.Host)), html.EscapeString(err.Error()), html.EscapeString(i18n.T("web.isItRunning")))
		},
	}
	return rp
}
