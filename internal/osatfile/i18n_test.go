package osatfile

import (
	"errors"
	"testing"

	"osat/internal/i18n"
)

// ErrBadPassword se traduce al mostrarse y sigue valiendo para errors.Is.
func TestErrBadPasswordFollowsLanguage(t *testing.T) {
	t.Cleanup(func() { i18n.SetLanguage(i18n.ES) })
	wrapped := errors.Join(errors.New("x"), ErrBadPassword)
	if !errors.Is(wrapped, ErrBadPassword) {
		t.Fatal("errors.Is no reconoce ErrBadPassword")
	}
	if got := ErrBadPassword.Error(); got != "contraseña incorrecta o archivo dañado" {
		t.Fatalf("es: %q", got)
	}
	i18n.SetLanguage(i18n.EN)
	if got := ErrBadPassword.Error(); got != "wrong password or damaged file" {
		t.Fatalf("en: %q", got)
	}
}
