//go:build linux && !android && cgo && webkit2_41 && osatsmoke

package browser

// Prueba de humo del motor de Linux (WebKitGTK): abre una página de login dentro de una ventana GTK, espera el
// evento "loaded" y prueba Eval, la detección de login y el relleno de sitefill.js. Necesita pantalla (WSLg sirve):
// go test -tags 'webkit2_41 osatsmoke' -run TestSmokeEngineLinux -v ./internal/browser/

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSmokeEngineLinux(t *testing.T) {
	stop, ok := smokeGTK()
	if !ok {
		t.Skip("sin pantalla")
	}
	defer stop()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>login</title></head><body><form>
<input id="u" name="username"><input id="p" type="password"><button>Entrar</button></form>
<script>document.getElementById("p").addEventListener("input", function () { document.title = "typed"; });</script></body></html>`)
	}))
	defer srv.Close()

	// No t.TempDir: WebKit puede seguir escribiendo el perfil un rato después de cerrar.
	profile, err := os.MkdirTemp("", "osat-smoke")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(profile)

	evs := make(chan Event, 64)
	eng, err := New(profile, func(e Event) { evs <- e })
	if err != nil {
		t.Fatal("New:", err)
	}
	defer eng.CloseAll()
	if err := eng.Open("t1", srv.URL+"/login", Bounds{X: 10, Y: 10, W: 600, H: 400}, 1); err != nil {
		t.Fatal("Open:", err)
	}
	deadline := time.After(30 * time.Second)
	for loaded := false; !loaded; {
		select {
		case e := <-evs:
			t.Logf("evento %+v", e)
			loaded = e.Kind == "loaded"
		case <-deadline:
			t.Fatal("no llegó el evento loaded")
		}
	}

	if got, err := eng.Eval("t1", `[document.title, 1 + 1]`); err != nil || got != `["login",2]` {
		t.Fatalf("Eval = %s, %v", got, err)
	}
	if _, err := eng.Eval("no-existe", "1"); err == nil {
		t.Fatal("Eval en una pestaña inexistente no falló")
	}

	src, err := os.ReadFile("../../frontend/src/lib/sitefill.js")
	if err != nil {
		t.Fatal(err)
	}
	script := strings.TrimSpace(string(src))
	if got, err := eng.Eval("t1", script+`({"probe":true})`); err != nil || got != `"login"` {
		t.Fatalf("probe = %s, %v", got, err)
	}
	fill := script + `({"domain":"127.0.0.1","account":"","user":"admin","password":"hunter2"})`
	if got, err := eng.Eval("t1", fill); err != nil || got != `"ok"` {
		t.Fatalf("relleno = %s, %v", got, err)
	}
	got, err := eng.Eval("t1", `[document.getElementById("u").value, document.getElementById("p").value, document.title]`)
	if err != nil || got != `["admin","hunter2","typed"]` {
		t.Fatalf("después del relleno = %s, %v", got, err)
	}
	other := strings.Replace(fill, `"127.0.0.1"`, `"empresa.com"`, 1)
	if got, err := eng.Eval("t1", other); err != nil || got != `"host"` {
		t.Fatalf("otro dominio = %s, %v", got, err)
	}
}
