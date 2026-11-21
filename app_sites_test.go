package main

import (
	"testing"

	"osat/internal/applock"
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
