//go:build !windows

package updater

// OpenUserInstall solo hace algo en Windows (apply_windows.go): fuera de ahí no hay copias viejas en Archivos de programa.
func OpenUserInstall() bool { return false }
