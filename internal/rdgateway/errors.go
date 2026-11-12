package rdgateway

import (
	"errors"
	"fmt"

	"osat/internal/i18n"
)

// Kind dice en qué paso y por qué falló el gateway, para explicárselo al usuario.
type Kind int

const (
	KindUnreachable       Kind = iota + 1 // el TCP al gateway no conectó (VPN caída, firewall)
	KindNoNTLM                            // el gateway no ofrece NTLM
	KindAuth                              // usuario o contraseña rechazados
	KindNotAuthorized                     // políticas del gateway o doble factor rechazado
	KindTimeout                           // no se aprobó a tiempo
	KindResourceDenied                    // el gateway no permite este equipo
	KindTargetUnreachable                 // el gateway no llegó al equipo
	KindProtocol                          // respuesta inesperada
	KindConnectionLost                    // la conexión se cortó después de establecerla (red inestable)
	KindDNS                               // no se encontró el nombre del gateway
	KindTLS                               // el TCP conectó pero no el TLS
	KindHTTP                              // el gateway contestó un estado HTTP inesperado
)

// kindNames: el nombre de cada Kind para el frontend y el log (no cambiar:
// el frontend arma la clave del texto con él).
var kindNames = map[Kind]string{
	KindUnreachable:       "unreachable",
	KindNoNTLM:            "noNtlm",
	KindAuth:              "auth",
	KindNotAuthorized:     "notAuthorized",
	KindTimeout:           "timeout",
	KindResourceDenied:    "resourceDenied",
	KindTargetUnreachable: "targetUnreachable",
	KindProtocol:          "protocol",
	KindConnectionLost:    "connectionLost",
	KindDNS:               "dns",
	KindTLS:               "tls",
	KindHTTP:              "http",
}

func (k Kind) String() string { return kindNames[k] }

// Pasos de la conexión, en orden (Error.Step).
const (
	StepDNS        = "dns"        // resolver el nombre del gateway
	StepTCP        = "tcp"        // conectar al puerto del gateway
	StepTLS        = "tls"        // TLS con el gateway
	StepHTTP       = "http"       // primer pedido HTTP (negociación NTLM)
	StepAuth       = "auth"       // autenticación NTLM y upgrade a WebSocket
	StepHandshake  = "handshake"  // handshake de MS-TSGU
	StepTunnel     = "tunnel"     // crear el túnel
	StepTunnelAuth = "tunnelAuth" // autorizar el túnel (CAP, doble factor)
	StepChannel    = "channel"    // canal hasta el equipo (RAP, alcance del equipo)
)

// Códigos HRESULT de MS-TSGU (§2.2.6.1) que se distinguen.
const (
	eProxyRAPAccessDenied = 0x800759DA
	eProxyNAPAccessDenied = 0x800759DB
	eProxyTSConnectFailed = 0x800759DD
)

// hresultNames: los HRESULT de MS-TSGU (§2.2.6.1), para los detalles
// técnicos. Se buscan por los 16 bits bajos: algunos gateways los mandan sin
// el 0x8007 de adelante.
var hresultNames = map[uint32]string{
	0x59D8: "E_PROXY_INTERNALERROR",
	0x59DA: "E_PROXY_RAP_ACCESSDENIED",
	0x59DB: "E_PROXY_NAP_ACCESSDENIED",
	0x59DD: "E_PROXY_TS_CONNECTFAILED",
	0x59DF: "E_PROXY_ALREADYDISCONNECTED",
	0x59E6: "E_PROXY_MAXCONNECTIONSREACHED",
	0x59E8: "E_PROXY_NOTSUPPORTED",
	0x59E9: "E_PROXY_CAPABILITYMISMATCH",
	0x59ED: "E_PROXY_QUARANTINE_ACCESSDENIED",
	0x59EE: "E_PROXY_NOCERTAVAILABLE",
	0x59F6: "E_PROXY_SESSIONTIMEOUT",
	0x59F7: "E_PROXY_COOKIE_BADPACKET",
	0x59F8: "E_PROXY_COOKIE_AUTHENTICATION_ACCESS_DENIED",
	0x59F9: "E_PROXY_UNSUPPORTED_AUTHENTICATION_METHOD",
	0x59FA: "E_PROXY_REAUTH_AUTHN_FAILED",
	0x04D4: "E_PROXY_CONNECTIONABORTED",
}

// HResultName: el nombre del código del gateway, o "" si no se conoce.
func HResultName(code uint32) string {
	if code == 0 {
		return ""
	}
	return hresultNames[code&0xFFFF]
}

// ErrChannelUsed: un túnel abre un solo canal; otro sería otra autorización.
var ErrChannelUsed = errors.New("rdgateway: el túnel ya abrió su canal")

type Error struct {
	Kind    Kind
	Step    string // en qué paso (Step*)
	Reason  string // matiz del Kind, si lo hay (p. ej. "refused" o "timeout" en el TCP)
	Gateway string // host:puerto
	Target  string // equipo:puerto, si el paso es el canal
	Code    uint32 // código del gateway, si lo hubo
	Status  int    // estado HTTP, si lo hubo
	Err     error
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindUnreachable:
		return i18n.T("app.rdgUnreachable", e.Gateway, e.Err)
	case KindDNS:
		return i18n.T("app.rdgDNS", e.Gateway, e.Err)
	case KindTLS:
		return i18n.T("app.rdgTLS", e.Gateway, e.Err)
	case KindHTTP:
		return i18n.T("app.rdgHTTP", e.Gateway, e.Status)
	case KindNoNTLM:
		return i18n.T("app.rdgNoNTLM", e.Gateway)
	case KindAuth:
		return i18n.T("app.rdgAuth", e.Gateway)
	case KindNotAuthorized:
		return i18n.T("app.rdgNotAuthorized", e.Gateway, e.Code)
	case KindTimeout:
		return i18n.T("app.rdgTimeout", e.Gateway)
	case KindResourceDenied:
		return i18n.T("app.rdgResourceDenied", e.Gateway, e.Target, e.Code)
	case KindConnectionLost:
		return i18n.T("app.rdgConnectionLost", e.Gateway, e.Err)
	case KindTargetUnreachable:
		return i18n.T("app.rdgTargetUnreachable", e.Gateway, e.Target, e.Code)
	}
	return i18n.T("app.rdgProtocol", e.Gateway, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Data: los datos para el frontend (que elige el texto por kind y reason) y
// para los detalles técnicos. Sin contraseñas: ningún paso las pone en Err.
func (e *Error) Data() map[string]string {
	d := map[string]string{"kind": e.Kind.String(), "step": e.Step, "gateway": e.Gateway}
	add := func(k, v string) {
		if v != "" {
			d[k] = v
		}
	}
	add("reason", e.Reason)
	add("target", e.Target)
	if e.Code != 0 {
		d["code"] = fmt.Sprintf("0x%08X", e.Code)
		add("codeName", HResultName(e.Code))
	}
	if e.Status != 0 {
		d["status"] = fmt.Sprint(e.Status)
	}
	if e.Err != nil {
		d["detail"] = e.Err.Error()
	}
	return d
}

// LogAttrs: lo mismo que Data, para slog.
func (e *Error) LogAttrs() []any {
	var attrs []any
	d := e.Data()
	for _, k := range []string{"step", "kind", "reason", "gateway", "target", "code", "codeName", "status", "detail"} {
		if v, ok := d[k]; ok {
			attrs = append(attrs, k, v)
		}
	}
	return attrs
}
