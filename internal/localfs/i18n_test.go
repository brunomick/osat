package localfs

import (
	"os"
	"testing"

	"osat/internal/i18n"
)

// Los errores de paquete y los de friendly siguen el idioma elegido.
func TestErrorsFollowLanguage(t *testing.T) {
	t.Cleanup(func() { i18n.SetLanguage(i18n.ES) })
	if got := friendly(os.ErrPermission).Error(); got != "acceso denegado" {
		t.Fatalf("es: %q", got)
	}
	i18n.SetLanguage(i18n.EN)
	if got := friendly(os.ErrPermission).Error(); got != "access denied" {
		t.Fatalf("en: %q", got)
	}
	if got := ErrTooBig.Error(); got != "the file is too big for the editor (max. 2 MB)" {
		t.Fatalf("ErrTooBig en: %q", got)
	}
}
