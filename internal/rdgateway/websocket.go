package rdgateway

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// WebSocket mínimo del lado cliente (RFC 6455). No se usa gorilla porque el
// gateway exige el upgrade con el método RDG_OUT_DATA, y gorilla solo manda GET.

const (
	opContinuation = 0x0
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// maxFrame: tope para no reservar lo que diga un largo corrupto.
const maxFrame = 16 << 20

func wsAcceptKey(key string) string {
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h[:])
}

// wsConn: las tramas sobre una conexión ya actualizada. br es el lector con
// el que se leyó la respuesta 101 (puede tener bytes del WebSocket adentro).
type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
	wmu  sync.Mutex
	left int64 // lo que falta leer de la trama de datos actual
}

func (w *wsConn) writeFrame(op byte, p []byte) error {
	hdr := []byte{0x80 | op, 0x80} // FIN, con máscara (obligatoria del cliente)
	switch n := len(p); {
	case n < 126:
		hdr[1] |= byte(n)
	case n <= 0xFFFF:
		hdr[1] |= 126
		hdr = binary.BigEndian.AppendUint16(hdr, uint16(n))
	default:
		hdr[1] |= 127
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	buf := append(append(hdr, mask...), p...)
	body := buf[len(hdr)+4:]
	for i := range body {
		body[i] ^= mask[i%4]
	}
	w.wmu.Lock()
	defer w.wmu.Unlock()
	_, err := w.conn.Write(buf)
	return err
}

// Read entrega los datos de las tramas de datos como un flujo de bytes:
// un paquete MS-TSGU puede venir partido en varias tramas, o varios en una.
// Contesta los ping y termina con io.EOF cuando llega un close.
func (w *wsConn) Read(p []byte) (int, error) {
	for w.left == 0 {
		op, n, err := w.readHeader()
		if err != nil {
			return 0, err
		}
		if op < opClose { // datos (binario, texto o continuación)
			w.left = n
			continue
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(w.br, payload); err != nil {
			return 0, err
		}
		switch op {
		case opPing:
			if err := w.writeFrame(opPong, payload); err != nil {
				return 0, err
			}
		case opClose:
			w.writeFrame(opClose, nil)
			return 0, io.EOF
		}
	}
	if int64(len(p)) > w.left {
		p = p[:w.left]
	}
	n, err := w.br.Read(p)
	w.left -= int64(n)
	return n, err
}

// readHeader lee el encabezado de una trama del servidor (sin máscara).
func (w *wsConn) readHeader() (op byte, n int64, err error) {
	var h [2]byte
	if _, err := io.ReadFull(w.br, h[:]); err != nil {
		return 0, 0, err
	}
	if h[1]&0x80 != 0 {
		return 0, 0, errors.New("trama WebSocket del servidor con máscara")
	}
	n = int64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		_, err = io.ReadFull(w.br, b[:])
		n = int64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		_, err = io.ReadFull(w.br, b[:])
		n = int64(binary.BigEndian.Uint64(b[:]))
	}
	if err != nil {
		return 0, 0, err
	}
	if n < 0 || n > maxFrame {
		return 0, 0, fmt.Errorf("trama WebSocket de %d bytes", n)
	}
	return h[0] & 0x0F, n, nil
}

// closeWait: lo que se espera para mandar los avisos de cierre. Con la red
// trabada, una escritura bloqueada (que tiene wmu) corta a ese tiempo.
const closeWait = time.Second

// closing pone un tiempo límite a las escrituras: lo que queda es cerrar.
func (w *wsConn) closing() { _ = w.conn.SetWriteDeadline(time.Now().Add(closeWait)) }

func (w *wsConn) Close() error {
	w.closing()
	w.writeFrame(opClose, nil)
	// Sin el close_notify de TLS: tls.Conn.Close lo manda con su propio
	// límite de 5 s, que con la red trabada se cumple entero.
	if tc, ok := w.conn.(*tls.Conn); ok {
		return tc.NetConn().Close()
	}
	return w.conn.Close()
}
