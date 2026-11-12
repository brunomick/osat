package remote

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
)

// Códigos de error estructurados que el frontend sabe interpretar.
const (
	CodeNeedSecret      = "NEED_SECRET"      // falta contraseña / passphrase
	CodeAuthFailed      = "AUTH_FAILED"      // credenciales rechazadas
	CodeHostKeyUnknown  = "HOSTKEY_UNKNOWN"  // primera conexión: confirmar huella
	CodeHostKeyMismatch = "HOSTKEY_MISMATCH" // la huella cambió (posible MITM)
	CodeConflict        = "CONFLICT"         // el destino de una transferencia ya existe
	CodeCancelled       = "CANCELLED"        // el usuario canceló una pregunta de autenticación
	CodeConnGone        = "CONN_GONE"        // la conexión (o la de su salto) ya no existe: no tiene sentido reintentar
	CodeNoTrash         = "NO_TRASH"         // no se puede mandar a la papelera: el frontend ofrece borrar definitivamente
	CodeRdpCertUnknown  = "RDPCERT_UNKNOWN"  // certificado RDP no válido para el sistema y nunca aceptado
	CodeRdpCertMismatch = "RDPCERT_MISMATCH" // el certificado RDP aceptado antes cambió
	CodeServerGone      = "SERVER_GONE"      // se cortó la conexión con el servidor: el frontend reconecta y reintenta
	CodeGateway         = "GATEWAY"          // falló un paso del RD Gateway (data: step, kind, reason, code...)
	CodeRdpServer       = "RDP_SERVER"       // falló un paso con el servidor RDP después del TCP (data.kind: x224, negotiation, tls)

	// Editar como root (ver sudo.go).
	CodePermission      = "PERMISSION"        // permiso denegado al leer o guardar por SFTP: el frontend ofrece sudo
	CodeSudoPassword    = "SUDO_PASSWORD"     // sudo pide contraseña
	CodeSudoBadPassword = "SUDO_BAD_PASSWORD" // la contraseña de sudo no es la correcta
)

// Error viaja al frontend como "OSAT:{json}" porque Wails serializa los
// errores de Go como strings.
type Error struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Data    map[string]string `json:"data,omitempty"`
}

func (e *Error) Error() string {
	b, _ := json.Marshal(e)
	return "OSAT:" + string(b)
}

func newErr(code, msg string, data map[string]string) *Error {
	return &Error{Code: code, Message: msg, Data: data}
}

// LogError registra un error de conexión. Lo que es parte del diálogo normal (pedir la
// contraseña, confirmar una huella o un certificado) va como detalle y el resto como
// aviso; un error estructurado se registra con su código y su mensaje, sin el JSON.
func LogError(msg string, err error, attrs ...any) {
	level := slog.LevelWarn
	var ke *Error
	if errors.As(err, &ke) {
		switch ke.Code {
		case CodeNeedSecret, CodeHostKeyUnknown, CodeRdpCertUnknown, CodeCancelled:
			level = slog.LevelDebug
		}
		attrs = append(attrs, "code", ke.Code, "err", ke.Message)
	} else {
		attrs = append(attrs, "err", err)
	}
	slog.Log(context.Background(), level, msg, attrs...)
}

func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "unable to authenticate") ||
		strings.Contains(s, "no supported methods remain")
}

// NewError crea un error estructurado (para otros paquetes, p. ej. RDP).
func NewError(code, msg string, data map[string]string) *Error { return newErr(code, msg, data) }
