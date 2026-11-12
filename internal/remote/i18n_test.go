package remote

import (
	"strings"
	"testing"

	"osat/internal/i18n"
)

// Los errores de paquete se traducen al mostrarse, no al crearse.
func TestPackageErrorsFollowLanguage(t *testing.T) {
	t.Cleanup(func() { i18n.SetLanguage(i18n.ES) })
	if got := errNoShell.Error(); got != "el servidor no permite ejecutar comandos" {
		t.Fatalf("es: %q", got)
	}
	i18n.SetLanguage(i18n.EN)
	if got := errNoShell.Error(); got != "the server doesn't allow running commands" {
		t.Fatalf("en: %q", got)
	}
	if got := missingTool("zip").Error(); !strings.Contains(got, "apt install zip") || !strings.Contains(got, "is not installed") {
		t.Fatalf("missingTool en: %q", got)
	}
	if got := keptWarning(3); !strings.Contains(got, "3 files") {
		t.Fatalf("keptWarning en: %q", got)
	}
}
