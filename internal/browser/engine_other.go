//go:build !windows && !(linux && !android && cgo && webkit2_41)

package browser

// Supported dice si este sistema tiene motor de navegador nativo.
func Supported() bool { return false }

// New crea el motor del sistema. Acá no hay: devuelve ErrUnsupported.
func New(profileDir string, emit func(Event)) (Engine, error) { return nil, ErrUnsupported }
