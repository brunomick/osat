package portfwd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// Proxy SOCKS5 (RFC 1928) sobre el mismo Listener: es el "-D" de ssh. Cada
// programa dice a dónde quiere ir y el destino se abre con dialAddr (en osat,
// desde el servidor SSH). Solo CONNECT, sin usuario ni contraseña.

// DialAddr abre una conexión a addr ("host:puerto"). El host puede ser un
// nombre: lo resuelve quien conecta (el servidor SSH).
type DialAddr func(ctx context.Context, addr string) (net.Conn, error)

type refusedError struct{ err error }

func (e refusedError) Error() string        { return e.err.Error() }
func (e refusedError) Unwrap() error        { return e.err }
func (e refusedError) Is(target error) bool { return target == ErrRefused }

// ErrRefused marca (con errors.Is) que el otro lado rechazó abrir la conexión
// (p. ej. el servidor SSH no pudo abrir el canal). El SOCKS lo contesta como
// "conexión rechazada" y no como un fallo cualquiera.
var ErrRefused = errors.New("conexión rechazada")

// Refused marca err como un rechazo (ver ErrRefused) sin cambiar su texto.
func Refused(err error) error { return refusedError{err} }

// Respuestas SOCKS5.
const (
	socksOK          = 0x00
	socksFailure     = 0x01
	socksRefused     = 0x05
	socksBadCommand  = 0x07
	socksBadAddrType = 0x08
)

// socksHandshakeTimeout es el plazo para el saludo y el pedido: un cliente
// colgado no se queda ocupando una conexión.
var socksHandshakeTimeout = 10 * time.Second // variable para acortarlo en los tests

// ListenSOCKS abre un proxy SOCKS5 en 127.0.0.1 (y ::1, como Listen) del puerto dado.
func ListenSOCKS(port int, dialAddr DialAddr) (*Listener, error) {
	return listen(port, func(ctx context.Context, local net.Conn) (net.Conn, error) {
		return socksConnect(ctx, local, dialAddr)
	})
}

// socksConnect hace el saludo y el pedido, abre el destino y contesta. Los
// errores ya se contestaron al cliente; Listener cierra la conexión.
func socksConnect(ctx context.Context, local net.Conn, dialAddr DialAddr) (net.Conn, error) {
	_ = local.SetDeadline(time.Now().Add(socksHandshakeTimeout))
	addr, err := socksRequest(local)
	if err != nil {
		return nil, err
	}
	remote, err := dialAddr(ctx, addr)
	if err != nil {
		code := byte(socksFailure)
		if errors.Is(err, ErrRefused) {
			code = socksRefused
		}
		_ = socksReply(local, code)
		return nil, err
	}
	if err := socksReply(local, socksOK); err != nil {
		remote.Close()
		return nil, err
	}
	_ = local.SetDeadline(time.Time{})
	return remote, nil
}

// socksRequest lee el saludo y el pedido; devuelve el destino "host:puerto".
// Lee justo lo necesario (sin buffer) para no tragarse datos del programa.
func socksRequest(c net.Conn) (string, error) {
	var head [4]byte
	if _, err := io.ReadFull(c, head[:2]); err != nil {
		return "", fmt.Errorf("SOCKS: saludo incompleto: %w", err)
	}
	if head[0] != 5 {
		return "", fmt.Errorf("SOCKS: versión %d no soportada (solo SOCKS5)", head[0])
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return "", fmt.Errorf("SOCKS: saludo incompleto: %w", err)
	}
	noAuth := false
	for _, m := range methods {
		noAuth = noAuth || m == 0x00
	}
	if !noAuth {
		_, _ = c.Write([]byte{5, 0xFF})
		return "", errors.New("SOCKS: el programa pide autenticación y el proxy no la usa")
	}
	if _, err := c.Write([]byte{5, 0x00}); err != nil {
		return "", err
	}

	if _, err := io.ReadFull(c, head[:]); err != nil {
		return "", fmt.Errorf("SOCKS: pedido incompleto: %w", err)
	}
	if head[0] != 5 {
		return "", fmt.Errorf("SOCKS: versión %d no soportada (solo SOCKS5)", head[0])
	}
	var host string
	switch head[3] {
	case 0x01, 0x04: // IPv4, IPv6
		ip := make(net.IP, 4)
		if head[3] == 0x04 {
			ip = make(net.IP, 16)
		}
		if _, err := io.ReadFull(c, ip); err != nil {
			return "", fmt.Errorf("SOCKS: pedido incompleto: %w", err)
		}
		host = ip.String()
	case 0x03: // nombre
		var n [1]byte
		if _, err := io.ReadFull(c, n[:]); err != nil {
			return "", fmt.Errorf("SOCKS: pedido incompleto: %w", err)
		}
		if n[0] == 0 {
			_ = socksReply(c, socksBadAddrType)
			return "", errors.New("SOCKS: nombre vacío")
		}
		name := make([]byte, n[0])
		if _, err := io.ReadFull(c, name); err != nil {
			return "", fmt.Errorf("SOCKS: pedido incompleto: %w", err)
		}
		host = string(name)
	default:
		_ = socksReply(c, socksBadAddrType)
		return "", fmt.Errorf("SOCKS: tipo de dirección %d no soportado", head[3])
	}
	var p [2]byte
	if _, err := io.ReadFull(c, p[:]); err != nil {
		return "", fmt.Errorf("SOCKS: pedido incompleto: %w", err)
	}
	// Se lee el pedido entero antes de contestar: cerrar con datos sin leer
	// puede hacer que el cliente pierda la respuesta.
	if head[1] != 0x01 {
		_ = socksReply(c, socksBadCommand)
		return "", fmt.Errorf("SOCKS: comando %d no soportado (solo CONNECT)", head[1])
	}
	return net.JoinHostPort(host, strconv.Itoa(int(p[0])<<8|int(p[1]))), nil
}

// socksReply contesta el pedido; la dirección enlazada es siempre 0.0.0.0:0.
func socksReply(c net.Conn, code byte) error {
	_, err := c.Write([]byte{5, code, 0, 0x01, 0, 0, 0, 0, 0, 0})
	return err
}
