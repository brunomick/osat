package web

import (
	"context"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// dirSource sirve una carpeta del disco.
type dirSource struct{ root string }

func (d dirSource) abs(name string) string { return filepath.Join(d.root, filepath.FromSlash(name)) }

func (d dirSource) Stat(name string) (fs.FileInfo, error) { return os.Stat(d.abs(name)) }
func (d dirSource) Open(name string) (File, error) {
	f, err := os.Open(d.abs(name))
	if err != nil {
		return nil, err
	}
	return f, nil
}
func (d dirSource) ReadDir(name string) ([]fs.FileInfo, error) {
	es, err := os.ReadDir(d.abs(name))
	if err != nil {
		return nil, err
	}
	out := make([]fs.FileInfo, 0, len(es))
	for _, e := range es {
		if fi, err := e.Info(); err == nil {
			out = append(out, fi)
		}
	}
	return out, nil
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fetch(t *testing.T, url string, mod ...func(*http.Request)) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mod {
		m(req)
	}
	cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestFilesServesAFolder(t *testing.T) {
	root := t.TempDir()
	write(t, root, "index.html", "<h1>hola</h1>")
	write(t, root, "css/app.css", "body{}")
	write(t, root, "js/app.js", "console.log(1)")
	write(t, root, "docs/a b.txt", "texto")
	write(t, root, "../secreto.txt", "no debería verse")

	hub := NewHub()
	defer hub.CloseAll()
	site, err := hub.Add("tab", Files(dirSource{root}), nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, body := fetch(t, site.URL+"/")
	if resp.StatusCode != 200 || body != "<h1>hola</h1>" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("index: %d %q %q", resp.StatusCode, body, resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("debería no cachear: %q", resp.Header.Get("Cache-Control"))
	}
	if resp, _ := fetch(t, site.URL+"/css/app.css"); !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("css: %q", resp.Header.Get("Content-Type"))
	}
	if resp, _ := fetch(t, site.URL+"/js/app.js"); !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") {
		t.Fatalf(".js debe salir como javascript aunque el registro de Windows diga otra cosa: %q", resp.Header.Get("Content-Type"))
	}

	// Rangos (videos, descargas parciales).
	resp, body = fetch(t, site.URL+"/css/app.css", func(r *http.Request) { r.Header.Set("Range", "bytes=0-1") })
	if resp.StatusCode != http.StatusPartialContent || body != "bo" {
		t.Fatalf("range: %d %q", resp.StatusCode, body)
	}

	// Una carpeta sin barra final redirige, y sin index.html se lista.
	resp, _ = fetch(t, site.URL+"/docs")
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "/docs/" {
		t.Fatalf("redirect: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if _, body = fetch(t, site.URL+"/docs/"); !strings.Contains(body, `href="a%20b.txt"`) || !strings.Contains(body, `href="../"`) {
		t.Fatalf("listado: %q", body)
	}

	// No se sale de la raíz ni con ../ ni con %2e%2e.
	for _, p := range []string{"/../secreto.txt", "/%2e%2e/secreto.txt", "/css/../../secreto.txt"} {
		resp, body := fetch(t, site.URL+p)
		if resp.StatusCode != 404 || strings.Contains(body, "no debería verse") {
			t.Fatalf("%s salió de la raíz: %d %q", p, resp.StatusCode, body)
		}
	}
	if resp, _ := fetch(t, site.URL+"/nada.html"); resp.StatusCode != 404 {
		t.Fatalf("inexistente: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, site.URL+"/", strings.NewReader("x"))
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST debería rechazarse: %v %v", resp, err)
	}
}

func TestSitesOnlyAnswerToTheirOwnAddress(t *testing.T) {
	hub := NewHub()
	defer hub.CloseAll()
	site, err := hub.Add("tab", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") }), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, body := fetch(t, site.URL+"/"); body != "ok" {
		t.Fatalf("con su dirección: %q", body)
	}
	// DNS rebinding: otro nombre que resuelve a 127.0.0.1.
	resp, _ := fetch(t, site.URL+"/", func(r *http.Request) { r.Host = "evil.example" })
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("con otro Host debería ser 403, fue %d", resp.StatusCode)
	}
	if !strings.HasPrefix(site.URL, "http://127.0.0.1:") {
		t.Fatalf("tiene que escuchar solo en loopback: %s", site.URL)
	}
}

func TestCloseOwnerClosesOnlyItsSites(t *testing.T) {
	hub := NewHub()
	defer hub.CloseAll()
	var closed atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })
	a1, _ := hub.Add("a", h, func() { closed.Add(1) })
	a2, _ := hub.Add("a", h, func() { closed.Add(1) })
	b, _ := hub.Add("b", h, func() { closed.Add(10) })

	hub.CloseOwner("a")
	if closed.Load() != 2 {
		t.Fatalf("done de los de a: %d", closed.Load())
	}
	for _, s := range []*Site{a1, a2} {
		if _, err := http.Get(s.URL + "/"); err == nil {
			t.Fatalf("%s debería estar cerrado", s.URL)
		}
	}
	if _, body := fetch(t, b.URL+"/"); body != "ok" {
		t.Fatal("el de b tenía que seguir andando")
	}
	hub.CloseOwner("a") // idempotente
	hub.CloseAll()
	if closed.Load() != 12 {
		t.Fatalf("done: %d", closed.Load())
	}
}

func TestProxyShowsTheTargetInAFrame(t *testing.T) {
	var gotHost, gotOrigin, gotReferer string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotOrigin, gotReferer = r.Host, r.Header.Get("Origin"), r.Header.Get("Referer")
		switch r.URL.Path {
		case "/go":
			http.Redirect(w, r, "http://"+r.Host+"/dest?x=1", http.StatusFound)
		default:
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; img-src *")
			io.WriteString(w, "hola desde el destino")
		}
	}))
	defer backend.Close()
	_, port, _ := net.SplitHostPort(backend.Listener.Addr().String())
	var p int
	for _, c := range port {
		p = p*10 + int(c-'0')
	}

	var dialed atomic.Int32
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	hub := NewHub()
	defer hub.CloseAll()
	site, err := hub.Add("tab", Proxy(Target{Host: "127.0.0.1", Port: p}, dial), nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, body := fetch(t, site.URL+"/", func(r *http.Request) {
		r.Header.Set("Origin", site.URL)
		r.Header.Set("Referer", site.URL+"/pagina")
	})
	if body != "hola desde el destino" {
		t.Fatalf("body: %q", body)
	}
	if dialed.Load() == 0 {
		t.Fatal("tiene que conectar con el dial que se le dio (la conexión SSH)")
	}
	if resp.Header.Get("X-Frame-Options") != "" {
		t.Fatal("X-Frame-Options impediría mostrarlo en el iframe")
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if strings.Contains(csp, "frame-ancestors") || !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "img-src *") {
		t.Fatalf("CSP: %q (solo debe perder frame-ancestors)", csp)
	}
	if gotHost != "127.0.0.1:"+port {
		t.Fatalf("el destino debe ver su propio Host, vio %q", gotHost)
	}
	if gotOrigin != "http://127.0.0.1:"+port || gotReferer != "http://127.0.0.1:"+port+"/" {
		t.Fatalf("Origin/Referer: %q %q", gotOrigin, gotReferer)
	}

	// Una redirección al nombre del destino no debe sacar la página del proxy.
	resp, _ = fetch(t, site.URL+"/go")
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/dest?x=1" {
		t.Fatalf("redirect: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Destino caído: una página que lo explica.
	backend.Close()
	resp, body = fetch(t, site.URL+"/")
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, "No se pudo abrir") {
		t.Fatalf("destino caído: %d %q", resp.StatusCode, body)
	}
}

func TestIsLoopback(t *testing.T) {
	for _, h := range []string{"localhost", "127.0.0.1", "127.5.5.5", "::1"} {
		if !IsLoopback(h) {
			t.Errorf("%s es loopback", h)
		}
	}
	for _, h := range []string{"", "example.com", "192.168.0.1", "0.0.0.0", "localhost.evil.com", "127.0.0.1.evil.com"} {
		if IsLoopback(h) {
			t.Errorf("%q no es loopback", h)
		}
	}
}

func TestTargetValid(t *testing.T) {
	for _, ok := range []Target{{Host: "localhost", Port: 3000}, {Scheme: "https", Host: "127.0.0.1", Port: 443}, {Host: "::1", Port: 80}, {Host: "10.0.0.5", Port: 8080}} {
		if err := ok.Valid(); err != nil {
			t.Errorf("%+v debería valer: %v", ok, err)
		}
	}
	for _, bad := range []Target{{Host: "localhost"}, {Host: "localhost", Port: 70000}, {Port: 80}, {Host: "a/b", Port: 80}, {Host: "u@h", Port: 80}, {Scheme: "ftp", Host: "h", Port: 21}} {
		if err := bad.Valid(); err == nil {
			t.Errorf("%+v no debería valer", bad)
		}
	}
}
