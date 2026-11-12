//go:build !windows

package applock

import "errors"

// Windows Hello solo existe en Windows: en Linux y macOS el desbloqueo es solo con contraseña.

// HelloAvailable siempre es false fuera de Windows.
func HelloAvailable() bool { return false }

// HelloVerify nunca verifica fuera de Windows.
func HelloVerify(hwnd uintptr, message string) (bool, error) {
	return false, errors.New("windows hello solo está disponible en Windows")
}

// WindowReady no importa fuera de Windows (no hay Hello).
func WindowReady() bool { return true }
