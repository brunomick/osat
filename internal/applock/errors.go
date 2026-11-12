package applock

import (
	"encoding/json"

	"osat/internal/i18n"
)

// Códigos de error que el frontend reconoce (nunca se reconoce un error por su texto).
const (
	CodeWrongPassword    = "LOCK_WRONG_PASSWORD"    // la contraseña no es la correcta
	CodeTooShort         = "LOCK_TOO_SHORT"         // la contraseña nueva es demasiado corta
	CodeStoreUnavailable = "LOCK_STORE_UNAVAILABLE" // el almacén de credenciales del sistema no responde
	CodeNotEnabled       = "LOCK_NOT_ENABLED"       // se pidió algo que necesita el bloqueo activado
)

// Error viaja al frontend como "OSAT:{json}", igual que los errores de remote, porque Wails
// serializa los errores de Go como strings.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	cause   error
}

func (e *Error) Error() string {
	b, _ := json.Marshal(e)
	return "OSAT:" + string(b)
}

func (e *Error) Unwrap() error { return e.cause }

func newErr(code, key string, args ...any) *Error {
	e := &Error{Code: code, Message: i18n.T(key, args...)}
	for _, a := range args {
		if err, ok := a.(error); ok {
			e.cause = err
			break
		}
	}
	return e
}
