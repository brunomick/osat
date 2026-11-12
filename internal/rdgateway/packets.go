package rdgateway

import (
	"encoding/binary"
	"fmt"
	"io"
	"unicode/utf16"
)

// Paquetes del transporte HTTP de MS-TSGU (§2.2.10), los mismos que viajan
// por el WebSocket. Todo en little-endian; encabezado de 8 bytes:
// tipo u16, reservado u16, largo total u32.
const (
	pktHandshakeRequest     = 0x01
	pktHandshakeResponse    = 0x02
	pktTunnelCreate         = 0x04
	pktTunnelResponse       = 0x05
	pktTunnelAuth           = 0x06
	pktTunnelAuthResponse   = 0x07
	pktChannelCreate        = 0x08
	pktChannelResponse      = 0x09
	pktData                 = 0x0A
	pktServiceMessage       = 0x0B
	pktKeepalive            = 0x0D
	pktCloseChannel         = 0x10
	pktCloseChannelResponse = 0x11
)

const (
	headerLen     = 8
	maxPacketLen  = headerLen + 2 + maxDataPerPkt // un DATA lleno
	maxDataPerPkt = 0xFFFF
)

type packet struct {
	typ  uint16
	body []byte
}

var le = binary.LittleEndian

// encode arma un paquete con su encabezado.
func encode(typ uint16, body []byte) []byte {
	b := make([]byte, headerLen, headerLen+len(body))
	le.PutUint16(b, typ)
	le.PutUint32(b[4:], uint32(headerLen+len(body)))
	return append(b, body...)
}

// utf16z: la cadena en UTF-16LE con terminador nulo.
func utf16z(s string) []byte {
	u := append(utf16.Encode([]rune(s)), 0)
	b := make([]byte, 2*len(u))
	for i, c := range u {
		le.PutUint16(b[2*i:], c)
	}
	return b
}

func encodeHandshakeRequest() []byte {
	// verMajor 1, verMinor 0, clientVersion 0, extendedAuth 0 (ninguna)
	return encode(pktHandshakeRequest, []byte{1, 0, 0, 0, 0, 0})
}

func encodeTunnelCreate() []byte {
	body := make([]byte, 8)
	// IDLE_TIMEOUT (0x02) + MESSAGING_SERVICE_MSG (0x08): sin SoH,
	// consentimiento, reautenticación ni UDP.
	le.PutUint32(body, 0x0A)
	return encode(pktTunnelCreate, body) // fieldsPresent 0, reservado 0
}

func encodeTunnelAuth(clientName string) []byte {
	name := utf16z(clientName)
	body := make([]byte, 4, 4+len(name))
	le.PutUint16(body[2:], uint16(len(name))) // fieldsPresent 0
	return encode(pktTunnelAuth, append(body, name...))
}

func encodeChannelCreate(host string, port int) []byte {
	name := utf16z(host)
	body := make([]byte, 8, 8+len(name))
	body[0] = 1 // un recurso, ninguno alternativo
	le.PutUint16(body[2:], uint16(port))
	le.PutUint16(body[4:], 3) // TCP
	le.PutUint16(body[6:], uint16(len(name)))
	return encode(pktChannelCreate, append(body, name...))
}

// encodeData: p no puede pasar de maxDataPerPkt bytes.
func encodeData(p []byte) []byte {
	body := make([]byte, 2, 2+len(p))
	le.PutUint16(body, uint16(len(p)))
	return encode(pktData, append(body, p...))
}

func encodeCloseChannel(status uint32) []byte {
	return encode(pktCloseChannel, le.AppendUint32(nil, status))
}

func encodeCloseChannelResponse(status uint32) []byte {
	return encode(pktCloseChannelResponse, le.AppendUint32(nil, status))
}

func readPacket(r io.Reader) (packet, error) {
	hdr := make([]byte, headerLen)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return packet{}, err
	}
	n := le.Uint32(hdr[4:])
	if n < headerLen || n > maxPacketLen {
		return packet{}, fmt.Errorf("paquete MS-TSGU de largo inválido (%d)", n)
	}
	body := make([]byte, n-headerLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return packet{}, err
	}
	return packet{typ: le.Uint16(hdr), body: body}, nil
}

// responseCode: el código de error o de estado de una respuesta (0 = bien).
func responseCode(p packet) (uint32, error) {
	off := 0
	switch p.typ {
	case pktHandshakeResponse, pktTunnelAuthResponse, pktChannelResponse, pktCloseChannel, pktCloseChannelResponse:
	case pktTunnelResponse:
		off = 2 // después de serverVersion
	default:
		return 0, fmt.Errorf("el paquete 0x%02X no es una respuesta", p.typ)
	}
	if len(p.body) < off+4 {
		return 0, fmt.Errorf("respuesta MS-TSGU 0x%02X demasiado corta", p.typ)
	}
	return le.Uint32(p.body[off:]), nil
}

// dataPayload: los datos de un paquete DATA.
func dataPayload(p packet) ([]byte, error) {
	if len(p.body) < 2 || int(le.Uint16(p.body)) > len(p.body)-2 {
		return nil, fmt.Errorf("paquete DATA inválido")
	}
	return p.body[2 : 2+int(le.Uint16(p.body))], nil
}
