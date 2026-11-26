package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"osat/internal/store"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x10")

func iconServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/icon.png":
			w.Write(pngBytes)
		case "/icon.svg":
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
		case "/login":
			w.Write([]byte("<html><body>no soy un ícono</body></html>"))
		case "/big.png":
			w.Write(append(pngBytes, make([]byte, siteIconMaxBytes)...))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchSiteIcon(t *testing.T) {
	srv := iconServer(t)
	c := &http.Client{Timeout: 5 * time.Second}
	// Se saltean los que no son imagen, los muy grandes y los que no existen; gana el primero que sirve.
	got := fetchSiteIcon(c, []string{srv.URL + "/login", srv.URL + "/big.png", srv.URL + "/no-hay", "file:///c:/x.png", srv.URL + "/icon.png", srv.URL + "/icon.svg"})
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("ícono = %.40s", got)
	}
	if got := fetchSiteIcon(c, []string{srv.URL + "/icon.svg"}); !strings.HasPrefix(got, "data:image/svg+xml;base64,") {
		t.Fatalf("svg = %.40s", got)
	}
	if got := fetchSiteIcon(c, []string{srv.URL + "/login"}); got != "" {
		t.Fatalf("una página no es un ícono: %.40s", got)
	}
}

func TestSiteIconRefresh(t *testing.T) {
	srv := iconServer(t)
	a := newExportApp(t)
	page := strings.TrimPrefix(srv.URL, "http://")
	host := page[:strings.Index(page, ":")]
	eng := &fakeEngine{out: `{"host":"` + host + `","urls":["` + srv.URL + `/icon.png"]}`}
	a.browser, a.bstate.on = eng, true
	st, err := a.SiteSave(store.Site{Name: "Panel", URL: srv.URL + "/login"})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := a.SiteIconRefresh("t1", st.ID); !changed || err != nil {
		t.Fatalf("primera vez: %v, %v", changed, err)
	}
	if got := a.SitesList()[0]; !strings.HasPrefix(got.Icon, "data:image/png") || got.IconAt == 0 {
		t.Fatalf("sitio = %+v", got)
	}

	// Recién bajado: no se vuelve a pedir a la página.
	eng.script = ""
	if changed, err := a.SiteIconRefresh("t1", st.ID); changed || err != nil || eng.script != "" {
		t.Fatalf("recién bajado: %v, %v, script %q", changed, err, eng.script)
	}

	// La página está en otro host (por ejemplo, el login de un proveedor): ese ícono no es del sitio.
	if err := a.store.SetSiteIcon(st.ID, "", 0); err != nil {
		t.Fatal(err)
	}
	eng.out = `{"host":"accounts.example.com","urls":["` + srv.URL + `/icon.png"]}`
	if changed, err := a.SiteIconRefresh("t1", st.ID); changed || err != nil {
		t.Fatalf("otro host: %v, %v", changed, err)
	}
	if got := a.SitesList()[0]; got.Icon != "" || got.IconAt != 0 {
		t.Fatalf("otro host no tenía que tocar el sitio: %+v", got)
	}
}
