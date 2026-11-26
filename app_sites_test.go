package main

import (
	"strings"
	"testing"

	"osat/internal/applock"
	"osat/internal/browser"
	"osat/internal/store"
)

func TestCredGetLocked(t *testing.T) {
	a := newExportApp(t)
	c, err := a.CredSave(store.CredInput{Label: "admin", User: "u", Password: "hunter2"})
	if err != nil {
		t.Fatal(err)
	}

	// Sin bloqueo (no hay contraseña): se lee.
	a.lock = applock.New(func(string, string) (string, error) { return "", nil }, nil, nil)
	if d, err := a.CredGet(c.ID); err != nil || d.Password != "hunter2" {
		t.Fatalf("desbloqueado: %+v, %v", d, err)
	}

	// Con un hash guardado, applock arranca bloqueado: no devuelve nada.
	a.lock = applock.New(func(string, string) (string, error) { return "hash", nil }, nil, nil)
	d, err := a.CredGet(c.ID)
	if err == nil || d != (store.CredData{}) {
		t.Fatalf("bloqueado devolvió %+v, %v", d, err)
	}
}

// fakeEngine solo implementa Eval: guarda el script y devuelve out.
type fakeEngine struct {
	browser.Engine
	script string
	out    string
}

func (f *fakeEngine) Eval(id, script string) (string, error) {
	f.script = script
	return f.out, nil
}

func TestBrowserFill(t *testing.T) {
	a := newExportApp(t)
	eng := &fakeEngine{out: `"ok"`}
	a.browser, a.bstate.on = eng, true
	c, err := a.CredSave(store.CredInput{Label: "panel", Domain: "panel.empresa.com", User: `u"</script>`, Password: "hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.BrowserFill("t1", c.ID, false); err != nil {
		t.Fatal(err)
	}
	// Los datos van como JSON al final del script, que es la función de sitefill.js.
	want := strings.TrimSpace(siteFillJS) + `({"domain":"panel.empresa.com","account":"","user":"u\"` + "\\u003c/script\\u003e" + `","password":"hunter2"})`
	if eng.script != want {
		t.Fatalf("script =\n%q\nquiero\n%q", eng.script[len(eng.script)-120:], want[len(want)-120:])
	}

	// Automático: el script recibe auto para no pisar lo escrito.
	if err := a.BrowserFill("t1", c.ID, true); err != nil || !strings.HasSuffix(eng.script, `"password":"hunter2","auto":true})`) {
		t.Fatalf("automático: %v, …%s", err, eng.script[len(eng.script)-60:])
	}

	for out, key := range map[string]string{`"host"`: "panel.empresa.com", `"form"`: "login", `null`: ""} {
		eng.out = out
		if err := a.BrowserFill("t1", c.ID, false); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("con %s: %v", out, err)
		}
	}

	// Sin dominio no se inyecta nada.
	nd, _ := a.CredSave(store.CredInput{Label: "sin dominio", Password: "x"})
	eng.script = ""
	if err := a.BrowserFill("t1", nd.ID, false); err == nil || eng.script != "" {
		t.Fatalf("sin dominio: %v, script %q", err, eng.script)
	}

	// Bloqueado tampoco.
	a.lock = applock.New(func(string, string) (string, error) { return "hash", nil }, nil, nil)
	if err := a.BrowserFill("t1", c.ID, false); err == nil || eng.script != "" {
		t.Fatalf("bloqueado: %v, script %q", err, eng.script)
	}
}

func TestBrowserHasLogin(t *testing.T) {
	a := newExportApp(t)
	eng := &fakeEngine{out: `"login"`}
	a.browser, a.bstate.on = eng, true
	if ok, err := a.BrowserHasLogin("t1"); !ok || err != nil {
		t.Fatalf("con login: %v, %v", ok, err)
	}
	if !strings.HasSuffix(eng.script, `({"probe":true})`) {
		t.Fatalf("el script no va en modo probe: …%s", eng.script[len(eng.script)-40:])
	}
	eng.out = `"none"`
	if ok, err := a.BrowserHasLogin("t1"); ok || err != nil {
		t.Fatalf("sin login: %v, %v", ok, err)
	}
}
