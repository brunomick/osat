package textenc

import (
	"bytes"
	"strings"
	"testing"
)

func TestDecodeUTF8(t *testing.T) {
	s, enc, ok := Decode([]byte("hola ñandú\r\n"))
	if !ok || enc != UTF8 || s != "hola ñandú\r\n" {
		t.Fatalf("Decode = %q, %v, %v", s, enc, ok)
	}
	if _, _, ok := Decode([]byte("a\x00b")); ok {
		t.Fatal("UTF-8 con NUL debería ser binario")
	}
	if s, enc, ok := Decode(nil); !ok || enc != UTF8 || s != "" {
		t.Fatalf("vacío: %q, %v, %v", s, enc, ok)
	}
}

func TestDecodeWindows1252(t *testing.T) {
	// "información" como lo guarda el Bloc de notas / Visual Studio.
	in := []byte("informaci\xf3n // \x80 \x93comillas\x94\r\n")
	s, enc, ok := Decode(in)
	if !ok || enc != Windows1252 {
		t.Fatalf("Decode = %q, %v, %v", s, enc, ok)
	}
	if want := "información // € “comillas”\r\n"; s != want {
		t.Fatalf("texto = %q, quería %q", s, want)
	}
	out, err := Encode(s, enc)
	if err != nil || !bytes.Equal(out, in) {
		t.Fatalf("round trip: %q, %v", out, err)
	}
}

// Cualquier secuencia de bytes que Decode acepte como Windows-1252 tiene que
// volver idéntica: es lo que garantiza que guardar no corrompe el archivo.
func TestWindows1252RoundTripsEveryByte(t *testing.T) {
	var all []byte
	for c := 0; c < 256; c++ {
		if isBinaryControl(byte(c)) {
			continue
		}
		all = append(all, byte(c))
	}
	s, enc, ok := Decode(all)
	if !ok || enc != Windows1252 {
		t.Fatalf("Decode: enc=%v ok=%v", enc, ok)
	}
	out, err := Encode(s, enc)
	if err != nil || !bytes.Equal(out, all) {
		t.Fatalf("round trip distinto (%v)", err)
	}
}

// El "Unicode" del Bloc de notas: UTF-16 LE con BOM (y BE, que es más raro).
func TestDecodeUTF16(t *testing.T) {
	for _, c := range []struct {
		enc Encoding
		in  []byte
	}{
		{UTF16LE, []byte{0xFF, 0xFE, 'h', 0, 'o', 0, 0xF1, 0, '\r', 0, '\n', 0, 0x3D, 0xD8, 0x00, 0xDE}},
		{UTF16BE, []byte{0xFE, 0xFF, 0, 'h', 0, 'o', 0, 0xF1, 0, '\r', 0, '\n', 0xD8, 0x3D, 0xDE, 0x00}},
	} {
		s, enc, ok := Decode(c.in)
		if !ok || enc != c.enc || s != "hoñ\r\n😀" {
			t.Fatalf("%v: Decode = %q, %v, %v", c.enc, s, enc, ok)
		}
		out, err := Encode(s, enc)
		if err != nil || !bytes.Equal(out, c.in) {
			t.Fatalf("%v: round trip = %v, %v", c.enc, out, err)
		}
	}
	// Solo el BOM: archivo vacío.
	if s, enc, ok := Decode([]byte{0xFF, 0xFE}); !ok || enc != UTF16LE || s != "" {
		t.Fatalf("solo BOM: %q, %v, %v", s, enc, ok)
	}
}

// Un log con bytes basura (0xFE 0x7F) entre el texto: no es UTF-8 válido, pero
// el Bloc de notas lo abre y osat también tiene que poder.
func TestDecodeLogWithDEL(t *testing.T) {
	in := []byte(`"EventID": "VA` + "\xfe\x7f" + `", "x": 1` + "\r\n")
	s, enc, ok := Decode(in)
	if !ok || enc != Windows1252 {
		t.Fatalf("Decode = %q, %v, %v", s, enc, ok)
	}
	if out, err := Encode(s, enc); err != nil || !bytes.Equal(out, in) {
		t.Fatalf("round trip = %q, %v", out, err)
	}
}

func TestDecodeRejectsBinary(t *testing.T) {
	// Sin ceros, pero con bytes de control: antes de la guarda pasaba por texto.
	for _, b := range [][]byte{
		{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, // cabecera PNG
		{0xFF, 0xD8, 0xFF, 0xE0, 0x01, 0x02},
		{0xFF, 0xFE, 'a', 0, 'b'},        // UTF-16 con un byte suelto al final
		{0xFF, 0xFE, 0x3D, 0xD8, 'a', 0}, // UTF-16 con un par sustituto cortado
		{0xFF, 0xFE, 'a', 0, 0, 0},       // UTF-16 con NUL
	} {
		if _, _, ok := Decode(b); ok {
			t.Errorf("%q debería ser binario", b)
		}
	}
}

func TestEncodeUnencodable(t *testing.T) {
	if _, err := Encode("flecha →", Windows1252); err == nil || !strings.Contains(err.Error(), "→") {
		t.Fatalf("err = %v", err)
	}
	out, err := Encode("flecha →", UTF8)
	if err != nil || string(out) != "flecha →" {
		t.Fatalf("UTF-8: %q, %v", out, err)
	}
}
