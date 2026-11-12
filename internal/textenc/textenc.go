// Package textenc decodifica y codifica el texto que edita el editor
// integrado: UTF-8 y, como alternativa, Windows-1252 (ANSI), que es lo que
// guardan Visual Studio, el Bloc de notas y otros editores viejos para los
// archivos con acentos (un .cpp con "ó" guardado como 0xF3, por ejemplo).
//
// Un archivo que no es UTF-8 válido se abre como Windows-1252 y se guarda de
// vuelta igual, byte por byte, así que el round trip no pierde nada. Lo mismo
// vale para UTF-16 con BOM (el "Unicode" del Bloc de notas).
package textenc

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Encoding es la codificación en la que estaba un archivo al leerlo.
type Encoding uint8

const (
	UTF8 Encoding = iota
	Windows1252
	UTF16LE // con BOM: lo que el Bloc de notas llama "Unicode"
	UTF16BE // con BOM
)

func (e Encoding) String() string {
	switch e {
	case Windows1252:
		return "Windows-1252"
	case UTF16LE:
		return "UTF-16 LE"
	case UTF16BE:
		return "UTF-16 BE"
	}
	return "UTF-8"
}

// win1252High da el carácter de cada byte 0x80–0x9F. Los cinco que Windows
// deja sin definir (0x81, 0x8D, 0x8F, 0x90, 0x9D) se mapean a su propio punto
// de código de control C1, para que la conversión sea biyectiva.
var win1252High = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

var win1252Rev = func() map[rune]byte {
	m := make(map[rune]byte, len(win1252High))
	for i, r := range win1252High {
		m[r] = byte(0x80 + i)
	}
	return m
}()

// Decode convierte los bytes de un archivo a texto. ok es false si parece
// binario (no es texto editable).
//
// UTF-8 válido se acepta salvo que tenga bytes NUL. Si no es UTF-8 válido se
// asume Windows-1252, pero solo si no tiene bytes de control raros: sin esa
// guarda casi cualquier binario sin ceros pasaría por texto.
func Decode(b []byte) (s string, enc Encoding, ok bool) {
	if len(b) >= 2 && (b[0] == 0xFF && b[1] == 0xFE || b[0] == 0xFE && b[1] == 0xFF) {
		return decodeUTF16(b)
	}
	if utf8.Valid(b) {
		if bytes.IndexByte(b, 0) >= 0 {
			return "", UTF8, false
		}
		return string(b), UTF8, true
	}
	for _, c := range b {
		if isBinaryControl(c) {
			return "", Windows1252, false
		}
	}
	rs := make([]rune, len(b))
	for i, c := range b {
		if c >= 0x80 && c < 0xA0 {
			rs[i] = win1252High[c-0x80]
		} else {
			rs[i] = rune(c)
		}
	}
	return string(rs), Windows1252, true
}

// decodeUTF16 lee un archivo que empieza con BOM UTF-16. Solo se acepta si al
// volver a codificarlo da los mismos bytes (sin pares sustitutos rotos ni un
// byte suelto al final) y no tiene NUL: si no, guardarlo lo corrompería.
func decodeUTF16(b []byte) (string, Encoding, bool) {
	enc := UTF16LE
	if b[0] == 0xFE {
		enc = UTF16BE
	}
	body := b[2:]
	if len(body)%2 != 0 {
		return "", enc, false
	}
	units := make([]uint16, len(body)/2)
	for i := range units {
		if enc == UTF16LE {
			units[i] = uint16(body[2*i]) | uint16(body[2*i+1])<<8
		} else {
			units[i] = uint16(body[2*i])<<8 | uint16(body[2*i+1])
		}
	}
	s := string(utf16.Decode(units))
	if strings.IndexByte(s, 0) >= 0 {
		return "", enc, false
	}
	if out, err := Encode(s, enc); err != nil || !bytes.Equal(out, b) {
		return "", enc, false
	}
	return s, enc, true
}

// isBinaryControl dice si c es un byte de control que no aparece en texto
// normal. Tab, saltos de línea, form feed, backspace y ESC (colores ANSI en
// logs) sí, y también DEL (0x7F): los logs de equipos lo traen suelto entre el
// texto y el Bloc de notas lo muestra sin quejarse.
func isBinaryControl(c byte) bool {
	switch {
	case c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\b' || c == 0x1B:
		return false
	default:
		return c < 0x20
	}
}

// Encode es la inversa de Decode. Falla si el texto tiene un carácter que no
// existe en la codificación (por ejemplo "→" en Windows-1252): en ese caso no
// se guarda nada, para no cambiarlo por un "?" en silencio.
func Encode(s string, enc Encoding) ([]byte, error) {
	if enc == UTF16LE || enc == UTF16BE {
		units := utf16.Encode([]rune(s))
		out := make([]byte, 0, 2+2*len(units))
		if enc == UTF16LE {
			out = append(out, 0xFF, 0xFE)
		} else {
			out = append(out, 0xFE, 0xFF)
		}
		for _, u := range units {
			if enc == UTF16LE {
				out = append(out, byte(u), byte(u>>8))
			} else {
				out = append(out, byte(u>>8), byte(u))
			}
		}
		return out, nil
	}
	if enc != Windows1252 {
		return []byte(s), nil
	}
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r < 0x80, r >= 0xA0 && r <= 0xFF:
			out = append(out, byte(r))
		default:
			c, ok := win1252Rev[r]
			if !ok {
				return nil, fmt.Errorf("el texto tiene %q, que no existe en Windows-1252, la codificación original del archivo: quitalo para poder guardar", r)
			}
			out = append(out, c)
		}
	}
	return out, nil
}
