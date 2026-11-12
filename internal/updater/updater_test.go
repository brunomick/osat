package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.5.0", "0.4.0", true},
		{"0.4.0", "0.5.0", false},
		{"0.4.0", "0.4.0", false},
		{"0.10.0", "0.9.3", true},
		{"v1.0.0", "0.99.99", true},
		{"0.4.1", "0.4.0", true},
		{"0.5.0-beta", "0.4.9", true},
		{"desconocida", "0.1.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, quería %v", c.a, c.b, got, c.want)
		}
	}
}

func testRelease() Release {
	return Release{Version: "0.5.0", Assets: []Asset{
		{Name: "osat-v0.5.0-linux-x64.tar.gz"},
		{Name: "osat-v0.5.0-macos-universal.zip"},
		{Name: "osat-v0.5.0-windows-x64-portable.zip"},
		{Name: "osat-v0.5.0-windows-x64-setup.exe"},
	}}
}

func TestPickAsset(t *testing.T) {
	rel := testRelease()
	want := map[Kind]string{
		KindWindowsPortable:  "osat-v0.5.0-windows-x64-portable.zip",
		KindWindowsInstaller: "osat-v0.5.0-windows-x64-setup.exe",
		KindLinux:            "osat-v0.5.0-linux-x64.tar.gz",
		KindMac:              "osat-v0.5.0-macos-universal.zip",
	}
	for k, name := range want {
		a, ok := PickAsset(rel, k)
		if !ok || a.Name != name {
			t.Errorf("PickAsset(%s) = %q, %v", k, a.Name, ok)
		}
	}
	if _, ok := PickAsset(Release{}, KindLinux); ok {
		t.Error("una Release sin archivos no debería tener uno para Linux")
	}
}

const changelog = `# Cambios

## [0.5.0] - 2026-11-12

### Agregado

- Uno.

## [0.4.0] - 2026-11-11

- Dos.
`

func TestChangelogSection(t *testing.T) {
	if got := ChangelogSection(changelog, "0.5.0"); got != "### Agregado\n\n- Uno." {
		t.Errorf("0.5.0: %q", got)
	}
	if got := ChangelogSection(strings.ReplaceAll(changelog, "\n", "\r\n"), "0.4.0"); got != "- Dos." {
		t.Errorf("0.4.0 con CRLF: %q", got)
	}
	if got := ChangelogSection(changelog, "9.9.9"); got != "" {
		t.Errorf("versión que no está: %q", got)
	}
}

func TestStripDownloads(t *testing.T) {
	if got := StripDownloads("### Agregado\n\n- Uno.\n\n## Descargas\n\n- Windows…"); got != "### Agregado\n\n- Uno." {
		t.Errorf("%q", got)
	}
	if got := StripDownloads("- Sin descargas"); got != "- Sin descargas" {
		t.Errorf("%q", got)
	}
}

func TestNotesFor(t *testing.T) {
	body := "### Agregado\r\n\r\n- Uno.\r\n\r\n## Descargas\r\n\r\n- Windows…\r\n\r\n---\r\n\r\n<!-- lang:en -->\r\n### Added\r\n\r\n- One.\r\n\r\n## Downloads\r\n\r\n- Windows…"
	if got := NotesFor(body, "es"); got != "### Agregado\n\n- Uno." {
		t.Errorf("es: %q", got)
	}
	if got := NotesFor(body, "en"); got != "### Added\n\n- One." {
		t.Errorf("en: %q", got)
	}
	// Sin parte en inglés (Releases anteriores), el inglés usa el español.
	if got := NotesFor("- Solo español\n\n## Descargas\n- x", "en"); got != "- Solo español" {
		t.Errorf("sin inglés: %q", got)
	}
}

func TestLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "osat/0.4.0" {
			t.Errorf("User-Agent: %q", r.Header.Get("User-Agent"))
		}
		w.Write([]byte(`{"tag_name":"v0.5.0","html_url":"https://x/r","body":"- Nuevo\r\n\r\n## Descargas\r\n- a",
			"assets":[{"name":"osat-v0.5.0-linux-x64.tar.gz","browser_download_url":"https://x/a","size":3}]}`))
	}))
	defer srv.Close()
	rel, err := Latest(context.Background(), srv.URL, "osat/0.4.0")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "0.5.0" || NotesFor(rel.Body, "es") != "- Nuevo" || rel.PageURL != "https://x/r" || len(rel.Assets) != 1 || rel.Assets[0].URL != "https://x/a" {
		t.Errorf("%+v", rel)
	}
}

func TestLatestErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/404" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"tag_name":"v0.5.0","prerelease":true}`))
	}))
	defer srv.Close()
	if _, err := Latest(context.Background(), srv.URL+"/404", "osat"); err == nil {
		t.Error("un 404 (repo privado) debería ser un error")
	}
	if _, err := Latest(context.Background(), srv.URL, "osat"); err == nil {
		t.Error("una prerelease no cuenta como versión publicada")
	}
}

func TestDownload(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 300<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Write(body)
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "f")
	var lastDone, lastTotal int64
	if err := Download(context.Background(), srv.URL, dest, "osat", func(d, tot int64) { lastDone, lastTotal = d, tot }); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, body) || lastDone != int64(len(body)) || lastTotal != int64(len(body)) {
		t.Errorf("descargado %d bytes, avance %d/%d", len(got), lastDone, lastTotal)
	}
}

func TestExtractZip(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "a.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("osat.exe")
	w.Write([]byte("nuevo"))
	w, _ = zw.Create("osat.app/Contents/MacOS/osat")
	w.Write([]byte("mac"))
	zw.Close()
	os.WriteFile(zp, buf.Bytes(), 0o644)

	dest := filepath.Join(dir, "osat.exe")
	if err := extractZipFile(zp, "osat.exe", dest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "nuevo" {
		t.Errorf("%q", b)
	}
	if err := extractZipFile(zp, "otro.exe", dest); err == nil {
		t.Error("un archivo que no está debería ser un error")
	}
	all := filepath.Join(dir, "todo")
	if err := extractZipAll(zp, all); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(all, "osat.app", "Contents", "MacOS", "osat")); string(b) != "mac" {
		t.Errorf("%q", b)
	}
}

func TestExtractZipRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "a.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("../afuera.txt")
	w.Write([]byte("x"))
	zw.Close()
	os.WriteFile(zp, buf.Bytes(), 0o644)
	if err := extractZipAll(zp, filepath.Join(dir, "d")); err == nil {
		t.Error("una ruta con ../ no debería salir de la carpeta")
	}
	if _, err := os.Stat(filepath.Join(dir, "afuera.txt")); err == nil {
		t.Error("se escribió afuera de la carpeta")
	}
}

func TestExtractTarGz(t *testing.T) {
	dir := t.TempDir()
	tp := filepath.Join(dir, "a.tar.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "osat-linux/", Typeflag: tar.TypeDir, Mode: 0o755})
	tw.WriteHeader(&tar.Header{Name: "osat-linux/osat", Typeflag: tar.TypeReg, Mode: 0o755, Size: 5})
	tw.Write([]byte("linux"))
	tw.Close()
	gz.Close()
	os.WriteFile(tp, buf.Bytes(), 0o644)
	dest := filepath.Join(dir, "osat")
	if err := extractTarGzFile(tp, "osat-linux/osat", dest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "linux" {
		t.Errorf("%q", b)
	}
}

func TestWritableDir(t *testing.T) {
	if !writableDir(t.TempDir()) {
		t.Error("la carpeta temporal debería poder escribirse")
	}
	if writableDir(filepath.Join(t.TempDir(), "no-existe")) {
		t.Error("una carpeta que no existe no se puede escribir")
	}
}
