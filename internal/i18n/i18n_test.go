package i18n

import (
	"errors"
	"regexp"
	"testing"
)

var verbs = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)

func TestCatalogComplete(t *testing.T) {
	for k, e := range catalog {
		if e.es == "" || e.en == "" {
			t.Errorf("%s: falta una traducción", k)
		}
		a, b := verbs.FindAllString(e.es, -1), verbs.FindAllString(e.en, -1)
		if len(a) != len(b) {
			t.Errorf("%s: los verbos %% difieren (%v / %v)", k, a, b)
			continue
		}
		for i := range a {
			if a[i] != b[i] {
				t.Errorf("%s: los verbos %% difieren (%v / %v)", k, a, b)
			}
		}
	}
}

func TestTAndHotSwitch(t *testing.T) {
	t.Cleanup(func() { SetLanguage(ES) })
	SetLanguage(ES)
	if got := T("example.hello", "Ana"); got != "Hola, Ana" {
		t.Fatalf("es: %q", got)
	}
	SetLanguage(EN)
	if got := T("example.hello", "Ana"); got != "Hello, Ana" {
		t.Fatalf("en: %q", got)
	}
	if got := T("no.existe"); got != "no.existe" {
		t.Fatalf("clave inexistente: %q", got)
	}
}

func TestSystemLanguage(t *testing.T) {
	t.Cleanup(func() { SetLanguage(ES) })
	t.Setenv("LC_ALL", "en_US.UTF-8")
	SetLanguage("system")
	if Language() != EN {
		t.Fatalf("LC_ALL=en_US: %q", Language())
	}
	t.Setenv("LC_ALL", "es_AR.UTF-8")
	SetLanguage("system")
	if Language() != ES {
		t.Fatalf("LC_ALL=es_AR: %q", Language())
	}
}

func TestErrTranslatesWhenAsked(t *testing.T) {
	t.Cleanup(func() { SetLanguage(ES) })
	catalog["test.wrapped"] = entry{es: "no se pudo leer la clave privada: %v", en: "couldn't read the private key: %v"}
	t.Cleanup(func() { delete(catalog, "test.wrapped") })
	inner := errors.New("boom")
	SetLanguage(ES)
	e := Err("test.wrapped", inner)
	if !errors.Is(e, inner) {
		t.Fatal("Err no envuelve el error")
	}
	if got := e.Error(); got != "no se pudo leer la clave privada: boom" {
		t.Fatalf("es: %q", got)
	}
	SetLanguage(EN) // el mismo error, ya creado, sale en el idioma nuevo
	if got := e.Error(); got != "couldn't read the private key: boom" {
		t.Fatalf("en: %q", got)
	}
}
