package vt

import (
	"fmt"
	"strings"
	"testing"
)

func write(s *Screen, text string) { s.Write([]byte(text)) }

func TestPlainLinesAndTrailingBlankLines(t *testing.T) {
	s := New(40, 10)
	write(s, "hola\r\nmundo\r\n")
	if got := s.Text(0); got != "hola\nmundo" {
		t.Fatalf("Text = %q", got)
	}
}

func TestCarriageReturnOverwritesAndEraseLine(t *testing.T) {
	s := New(40, 5)
	write(s, "abcdef\rXY")
	if got := s.Text(0); got != "XYcdef" {
		t.Fatalf("\\r debería sobrescribir: %q", got)
	}
	write(s, "\x1b[K") // borra del cursor al final
	if got := s.Text(0); got != "XY" {
		t.Fatalf("EL: %q", got)
	}
	// Barra de progreso típica: \r + texto nuevo + borrar el resto.
	write(s, "\r\x1b[2Kcopiando 50%\r\x1b[2Kcopiando 100%")
	if got := s.Text(0); got != "copiando 100%" {
		t.Fatalf("progreso: %q", got)
	}
}

func TestIgnoresColorsAndTitles(t *testing.T) {
	s := New(60, 5)
	write(s, "\x1b]0;título de la ventana\x07\x1b[01;32mubuntu@ip\x1b[0m:\x1b[01;34m~\x1b[0m$ ls\r\n")
	write(s, "\x1b]8;;http://x\x1b\\enlace\x1b]8;;\x1b\\\r\n")
	if got := s.Text(0); got != "ubuntu@ip:~$ ls\nenlace" {
		t.Fatalf("Text = %q", got)
	}
}

func TestCursorPositioningLikeConPTY(t *testing.T) {
	// ConPTY redibuja con posiciones absolutas: no debe duplicar el texto.
	s := New(30, 6)
	write(s, "\x1b[2J\x1b[H")
	write(s, "\x1b[1;1HC:\\Users>dir\x1b[2;1Hhola.txt\x1b[3;1HC:\\Users>")
	write(s, "\x1b[1;1HC:\\Users>dir") // redibujo de la misma línea
	if got := s.Text(0); got != "C:\\Users>dir\nhola.txt\nC:\\Users>" {
		t.Fatalf("Text = %q", got)
	}
	write(s, "\x1b[3;10H") // cursor justo después del prompt de 9 caracteres
	write(s, "echo")
	if got := s.Text(0); got != "C:\\Users>dir\nhola.txt\nC:\\Users>echo" {
		t.Fatalf("Text = %q", got)
	}
}

func TestScrollbackKeepsLinesThatLeftTheScreen(t *testing.T) {
	s := New(20, 3)
	for i := 1; i <= 10; i++ {
		write(s, fmt.Sprintf("línea %d\r\n", i))
	}
	all := s.Text(0)
	if !strings.HasPrefix(all, "línea 1\nlínea 2\n") || !strings.HasSuffix(all, "línea 10") {
		t.Fatalf("Text = %q", all)
	}
	if got := s.Text(3); got != "línea 8\nlínea 9\nlínea 10" {
		t.Fatalf("últimas 3 = %q", got)
	}
}

func TestLongLinesWrap(t *testing.T) {
	s := New(10, 4)
	write(s, "0123456789ABCDE\r\n")
	if got := s.Text(0); got != "0123456789\nABCDE" {
		t.Fatalf("Text = %q", got)
	}
}

func TestUTF8SplitAcrossWrites(t *testing.T) {
	s := New(20, 3)
	b := []byte("ñandú ✓")
	s.Write(b[:1]) // parte de la ñ
	s.Write(b[1:3])
	s.Write(b[3:])
	if got := s.Text(0); got != "ñandú ✓" {
		t.Fatalf("Text = %q", got)
	}
}

func TestAlternateScreenIsRestored(t *testing.T) {
	s := New(20, 4)
	write(s, "$ vim\r\n")
	write(s, "\x1b[?1049h\x1b[Hdentro de vim")
	if got := s.Text(0); got != "dentro de vim" {
		t.Fatalf("en la pantalla alternativa: %q", got)
	}
	if !s.InAltScreen() {
		t.Fatal("debería saber que hay un programa de pantalla completa")
	}
	write(s, "\x1b[?1049l")
	if s.InAltScreen() {
		t.Fatal("al salir ya no está en la pantalla alternativa")
	}
	if got := s.Text(0); got != "$ vim" {
		t.Fatalf("al salir debería volver lo anterior: %q", got)
	}
}

func TestMarkAndSinceReturnOnlyNewOutput(t *testing.T) {
	s := New(30, 4)
	write(s, "viejo 1\r\nviejo 2\r\n$ ")
	mark := s.Mark() // la línea del prompt
	write(s, "ls\r\na.txt\r\nb.txt\r\n$ ")
	if got := s.Since(mark); got != "$ ls\na.txt\nb.txt\n$" {
		t.Fatalf("Since = %q", got)
	}
	// La marca sigue valiendo aunque las líneas se hayan ido al historial.
	for i := 0; i < 10; i++ {
		write(s, "x\r\n")
	}
	if got := s.Since(mark); !strings.HasPrefix(got, "$ ls\na.txt\nb.txt") {
		t.Fatalf("Since con scroll = %q", got)
	}
}

func TestResizeKeepsContent(t *testing.T) {
	s := New(20, 4)
	write(s, "uno\r\ndos\r\ntres\r\n")
	s.Resize(10, 2)
	if got := s.Text(0); got != "uno\ndos\ntres" {
		t.Fatalf("después de achicar: %q", got)
	}
	s.Resize(40, 6)
	write(s, "cuatro\r\n")
	if got := s.Text(0); got != "uno\ndos\ntres\ncuatro" {
		t.Fatalf("después de agrandar: %q", got)
	}
}

func TestInsertAndDeleteLinesAndChars(t *testing.T) {
	s := New(20, 5)
	write(s, "a\r\nb\r\nc")
	write(s, "\x1b[2;1H\x1b[L") // inserta una línea vacía antes de b
	write(s, "X")
	if got := s.Text(0); got != "a\nX\nb\nc" {
		t.Fatalf("insertar línea: %q", got)
	}
	write(s, "\x1b[2;1H\x1b[M") // la borra
	if got := s.Text(0); got != "a\nb\nc" {
		t.Fatalf("borrar línea: %q", got)
	}
	write(s, "\x1b[1;1H\x1b[P") // borra el carácter bajo el cursor
	if got := s.Text(0); got != "\nb\nc" {
		t.Fatalf("borrar carácter: %q", got)
	}
}

func TestPromptRedrawWithBackspaces(t *testing.T) {
	// readline borra lo tipeado con retrocesos y espacios.
	s := New(30, 3)
	write(s, "$ ls -l")
	write(s, "\b\b\b\b\b     \b\b\b\b\b")
	write(s, "pwd")
	if got := s.Text(0); got != "$ pwd" {
		t.Fatalf("Text = %q", got)
	}
}
