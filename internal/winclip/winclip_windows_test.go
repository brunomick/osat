//go:build windows

package winclip

import (
	"encoding/binary"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func descriptor(name string, flags, attrs uint32, size int64, mt time.Time) []byte {
	d := make([]byte, fileDescriptorSize)
	binary.LittleEndian.PutUint32(d[0:], flags)
	binary.LittleEndian.PutUint32(d[36:], attrs)
	ft := windows.NsecToFiletime(mt.UnixNano())
	binary.LittleEndian.PutUint32(d[56:], ft.LowDateTime)
	binary.LittleEndian.PutUint32(d[60:], ft.HighDateTime)
	binary.LittleEndian.PutUint32(d[64:], uint32(size>>32))
	binary.LittleEndian.PutUint32(d[68:], uint32(size))
	for i, c := range utf16.Encode([]rune(name)) {
		binary.LittleEndian.PutUint16(d[nameOffset+2*i:], c)
	}
	return d
}

func TestParseDescriptors(t *testing.T) {
	mt := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, 2)
	b = append(b, descriptor("carpeta", fdAttributes, attrDir, 0, time.Time{})...)
	b = append(b, descriptor(`carpeta\año.txt`, fdAttributes|fdWritesTime|fdFileSize, 0, 5_000_000_000, mt)...)

	es, err := parseDescriptors(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 {
		t.Fatalf("%d entradas; quería 2", len(es))
	}
	if es[0].Name != "carpeta" || !es[0].Dir || es[0].size != -1 {
		t.Fatalf("carpeta = %+v", es[0])
	}
	if es[1].Name != `carpeta\año.txt` || es[1].Dir || es[1].size != 5_000_000_000 || !es[1].ModTime.Equal(mt) {
		t.Fatalf("archivo = %+v", es[1])
	}
}

func TestParseDescriptorsTruncated(t *testing.T) {
	b := make([]byte, 4+10)
	binary.LittleEndian.PutUint32(b, 1)
	if _, err := parseDescriptors(b); err == nil {
		t.Fatal("quería un error con el descriptor incompleto")
	}
}
