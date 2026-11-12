//go:build !windows

package osdrag

import (
	"osat/internal/i18n"
)

// ErrCancelled: el usuario soltó fuera de un destino válido o canceló.
var ErrCancelled = i18n.Err("app.dragCancelled")

// Supported: todavía no hay arrastre hacia el sistema fuera de Windows.
const Supported = false

// Files todavía no está implementado fuera de Windows.
func Files(paths []string) error {
	return i18n.Err("app.draggingFilesToTheSystem")
}
