package main

import (
	"os"
	"os/exec"

	"osat/internal/updater"
)

// openUserInstall abre el osat instalado para el usuario si este es la copia vieja de Archivos de
// programa (ver updater.UserInstallExe). Devuelve true si lo abrió y este tiene que cerrarse.
func openUserInstall() bool {
	target, ok := updater.UserInstallExe()
	if !ok {
		return false
	}
	return exec.Command(target, os.Args[1:]...).Start() == nil
}
