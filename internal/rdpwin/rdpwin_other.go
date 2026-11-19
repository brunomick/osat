//go:build !windows

package rdpwin

// Supported dice si este sistema tiene el cliente de Escritorio remoto de Windows.
func Supported() bool { return false }

// New no tiene nada que crear fuera de Windows.
func New(parent func() uintptr, emit func(Event)) (Engine, error) { return nil, ErrUnsupported }
