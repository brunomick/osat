package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"osat/internal/syncplan"
)

// syncRig: un Manager con la sesión SFTP "srv" sobre un sistema de archivos en memoria (con /srv/www) y
// una carpeta local vacía. run nil = el servidor no deja ejecutar comandos.
func syncRig(t *testing.T, run func(cmd string, ch ssh.Channel) int) (*Manager, *sftp.Client, string) {
	t.Helper()
	m := newBridgeManager(t)
	t.Cleanup(m.CloseAll)
	cl := memSFTP(t)
	client := forwardingServer(t)
	if run != nil {
		client = execServer(t, run)
	}
	m.register(&Session{ID: "srv", Kind: KindSFTP, ConnID: "c", sftp: cl, shared: newSharedClient(client)})
	if err := cl.MkdirAll("/srv/www"); err != nil {
		t.Fatal(err)
	}
	return m, cl, t.TempDir()
}

func writeLocal(t *testing.T, root, rel, content string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

func readLocal(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func syncReq(local, dir, mode, compare string) SyncRequest {
	return SyncRequest{RemoteDir: "/srv/www", LocalDir: local, Direction: dir, Mode: mode, Compare: compare}
}

// ops resume el plan como "op rel" (sin los motivos).
func ops(acts []syncplan.Action) []string {
	out := make([]string, len(acts))
	for i, a := range acts {
		out[i] = a.Op + " " + a.Rel
	}
	return out
}

func rels(acts []syncplan.Action) []string {
	var out []string
	for _, a := range acts {
		if a.Op != syncplan.OpSkip {
			out = append(out, a.Rel)
		}
	}
	return out
}

// runAll ejecuta todo el plan y espera a que termine.
func runAll(t *testing.T, m *Manager, p SyncPreview) Transfer {
	t.Helper()
	id, err := m.SyncRun("srv", p.PlanID, rels(p.Actions))
	if err != nil {
		t.Fatal(err)
	}
	info := waitTransfer(t, m, id)
	if info.Status != StatusDone {
		t.Fatalf("la sincronización terminó con %s: %s", info.Status, info.Error)
	}
	return info
}

func compare(t *testing.T, m *Manager, req SyncRequest) SyncPreview {
	t.Helper()
	p, err := m.SyncCompare("srv", req)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSyncOptionsValidation(t *testing.T) {
	ok := SyncRequest{Direction: "up", Mode: "sync", Compare: "time+size", Exclude: []string{" .git ", "", "node_modules"}}
	o, err := syncOptions(ok)
	if err != nil || strings.Join(o.Exclude, ",") != ".git,node_modules" {
		t.Fatalf("syncOptions = %+v, %v", o, err)
	}
	for _, c := range []struct {
		name string
		f    func(*SyncRequest)
	}{
		{"dirección", func(r *SyncRequest) { r.Direction = "sideways" }},
		{"dirección vacía", func(r *SyncRequest) { r.Direction = "" }},
		{"modo", func(r *SyncRequest) { r.Mode = "copy" }},
		{"criterio", func(r *SyncRequest) { r.Compare = "md5" }},
		{"espejo en ambas", func(r *SyncRequest) { r.Direction, r.Mode = "both", "mirror" }},
		{"borrar en ambas", func(r *SyncRequest) { r.Direction, r.Delete = "both", true }},
		{"patrón inválido", func(r *SyncRequest) { r.Exclude = []string{"["} }},
	} {
		r := ok
		c.f(&r)
		if _, err := syncOptions(r); err == nil {
			t.Errorf("%s: esperaba un error", c.name)
		}
	}
	m, _, local := syncRig(t, nil)
	bad := syncReq(local, "up", "mirror", "time+size")
	bad.Direction = "both"
	if _, err := m.SyncCompare("srv", bad); err == nil {
		t.Fatal("SyncCompare tenía que rechazar espejo en ambas direcciones")
	}
	for _, r := range []SyncRequest{
		syncReq("", "up", "sync", "time+size"),
		syncReq("relativa/x", "up", "sync", "time+size"),
		syncReq(filepath.Join(local, "no-existe"), "up", "sync", "time+size"),
		{RemoteDir: "/srv/nada", LocalDir: local, Direction: "up", Mode: "sync", Compare: "size"},
	} {
		if _, err := m.SyncCompare("srv", r); err == nil {
			t.Errorf("SyncCompare(%+v): esperaba un error", r)
		}
	}
}

func TestSyncUpload(t *testing.T) {
	m, cl, local := syncRig(t, nil)
	writeLocal(t, local, "a.txt", "hola", time.Time{})
	writeLocal(t, local, "sub/b.txt", "chau!", time.Time{})
	writeLocal(t, local, "node_modules/x.js", "x", time.Time{})
	writeLocal(t, local, ".git/config", "y", time.Time{})
	writeLocal(t, local, "app.log", "z", time.Time{})
	os.Mkdir(filepath.Join(local, "vacia"), 0o755)
	if err := os.Symlink("a.txt", filepath.Join(local, "enlace")); err != nil {
		t.Fatal(err)
	}

	req := syncReq(local, "up", "sync", "time+size")
	req.Exclude = []string{".git", "node_modules", "*.log"}
	p := compare(t, m, req)
	sameStrings(t, "plan", ops(p.Actions), []string{
		"upload a.txt", "skip enlace", "mkdirRemote sub", "upload sub/b.txt", "mkdirRemote vacia",
	})
	if p.RemoteDir != "/srv/www" || p.LocalDir != local {
		t.Fatalf("carpetas: %q %q", p.RemoteDir, p.LocalDir)
	}
	info := runAll(t, m, p)
	if info.Name != "Sincronización: 4 archivos" || info.Total != 9 || info.Done != 9 || info.Direction != "up" ||
		info.Source != local || info.Target != "/srv/www" {
		t.Fatalf("transferencia: %+v", info)
	}
	if get(t, cl, "/srv/www/a.txt") != "hola" || get(t, cl, "/srv/www/sub/b.txt") != "chau!" {
		t.Fatal("el contenido subido no coincide")
	}
	if fi, err := cl.Stat("/srv/www/vacia"); err != nil || !fi.IsDir() {
		t.Fatalf("la carpeta vacía no se creó: %v", err)
	}
	for _, p := range []string{"/srv/www/node_modules", "/srv/www/.git", "/srv/www/app.log", "/srv/www/enlace"} {
		if remoteExists(cl, p) {
			t.Errorf("se subió %s", p)
		}
	}
	assertNoTemps(t, cl, "/srv/www")
}

// Bajar deja la fecha del servidor en cada archivo: una segunda comparación no encuentra nada.
func TestSyncDownloadKeepsModTime(t *testing.T) {
	m, cl, local := syncRig(t, nil)
	cl.MkdirAll("/srv/www/d/vacia")
	put(t, cl, "/srv/www/a.txt", "uno")
	put(t, cl, "/srv/www/d/b.txt", "dos")
	writeLocal(t, local, "viejo.txt", "se queda", time.Time{}) // sin borrar, lo que sobra en el destino queda

	req := syncReq(local, "down", "sync", "time+size")
	p := compare(t, m, req)
	sameStrings(t, "plan", ops(p.Actions), []string{"download a.txt", "mkdirLocal d", "download d/b.txt", "mkdirLocal d/vacia"})
	runAll(t, m, p)
	if readLocal(t, local, "a.txt") != "uno" || readLocal(t, local, "d/b.txt") != "dos" || readLocal(t, local, "viejo.txt") != "se queda" {
		t.Fatal("el contenido bajado no coincide")
	}
	for _, rel := range []string{"a.txt", "d/b.txt"} {
		rfi, _ := cl.Stat("/srv/www/" + rel)
		lfi, err := os.Stat(filepath.Join(local, filepath.FromSlash(rel)))
		if err != nil || lfi.ModTime().Unix() != rfi.ModTime().Unix() {
			t.Errorf("%s: fecha local %v, en el servidor %v", rel, lfi.ModTime(), rfi.ModTime())
		}
	}
	if fi, err := os.Stat(filepath.Join(local, "d", "vacia")); err != nil || !fi.IsDir() {
		t.Fatalf("la carpeta vacía no se creó: %v", err)
	}
	for _, mode := range []string{"sync", "mirror"} {
		if again := compare(t, m, syncReq(local, "down", mode, "time+size")); len(again.Actions) != 0 {
			t.Fatalf("segunda comparación (%s): %q", mode, ops(again.Actions))
		}
	}
}

func TestSyncBothWays(t *testing.T) {
	m, cl, local := syncRig(t, nil)
	put(t, cl, "/srv/www/nuevo-en-servidor.txt", "srv") // fecha: ahora
	put(t, cl, "/srv/www/solo-servidor.txt", "s")
	put(t, cl, "/srv/www/nuevo-en-pc.txt", "viejo")
	writeLocal(t, local, "nuevo-en-servidor.txt", "pc viejo", time.Now().Add(-time.Hour))
	writeLocal(t, local, "nuevo-en-pc.txt", "pc", time.Now().Add(time.Hour))
	writeLocal(t, local, "solo-pc.txt", "p", time.Time{})

	p := compare(t, m, syncReq(local, "both", "sync", "time+size"))
	sameStrings(t, "plan", ops(p.Actions), []string{
		"upload nuevo-en-pc.txt", "download nuevo-en-servidor.txt", "upload solo-pc.txt", "download solo-servidor.txt",
	})
	if info := runAll(t, m, p); info.Direction != "both" {
		t.Fatalf("dirección: %q", info.Direction)
	}
	if get(t, cl, "/srv/www/nuevo-en-pc.txt") != "pc" || get(t, cl, "/srv/www/solo-pc.txt") != "p" ||
		readLocal(t, local, "nuevo-en-servidor.txt") != "srv" || readLocal(t, local, "solo-servidor.txt") != "s" {
		t.Fatal("el contenido no coincide")
	}
}

// Espejo con borrado al subir: el servidor queda igual a la PC, salvo lo excluido.
func TestSyncMirrorDeletesRemote(t *testing.T) {
	m, cl, local := syncRig(t, nil)
	writeLocal(t, local, "a.txt", "de la pc", time.Now().Add(-time.Hour)) // más viejo que el del servidor
	cl.MkdirAll("/srv/www/viejo/sub")
	cl.MkdirAll("/srv/www/node_modules")
	put(t, cl, "/srv/www/a.txt", "del servidor")
	put(t, cl, "/srv/www/sobra.txt", "x")
	put(t, cl, "/srv/www/viejo/sub/c.txt", "c")
	put(t, cl, "/srv/www/node_modules/m.js", "m")

	req := syncReq(local, "up", "mirror", "time+size")
	req.Delete, req.Exclude = true, []string{"node_modules"}
	p := compare(t, m, req)
	sameStrings(t, "plan", ops(p.Actions), []string{
		"upload a.txt", "deleteRemote viejo/sub/c.txt", "deleteRemote viejo/sub", "deleteRemote viejo", "deleteRemote sobra.txt",
	})
	runAll(t, m, p)
	if get(t, cl, "/srv/www/a.txt") != "de la pc" {
		t.Fatal("espejo: no se reemplazó el archivo más nuevo del servidor")
	}
	for _, gone := range []string{"/srv/www/sobra.txt", "/srv/www/viejo"} {
		if remoteExists(cl, gone) {
			t.Errorf("no se borró %s", gone)
		}
	}
	if get(t, cl, "/srv/www/node_modules/m.js") != "m" {
		t.Fatal("se tocó una carpeta excluida")
	}
}

func TestSyncMirrorDeletesLocal(t *testing.T) {
	m, cl, local := syncRig(t, nil)
	put(t, cl, "/srv/www/a.txt", "a")
	writeLocal(t, local, "a.txt", "a", time.Now().Add(time.Hour)) // más nuevo en la PC: espejo igual lo baja
	writeLocal(t, local, "sobra/x.txt", "x", time.Time{})
	writeLocal(t, local, ".git/HEAD", "h", time.Time{})

	req := syncReq(local, "down", "mirror", "time+size")
	req.Delete, req.Exclude = true, []string{".git"}
	p := compare(t, m, req)
	sameStrings(t, "plan", ops(p.Actions), []string{"download a.txt", "deleteLocal sobra/x.txt", "deleteLocal sobra"})
	runAll(t, m, p)
	if _, err := os.Stat(filepath.Join(local, "sobra")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no se borró la carpeta local: %v", err)
	}
	if readLocal(t, local, ".git/HEAD") != "h" {
		t.Fatal("se tocó una carpeta excluida")
	}
}

// Se ejecuta solo lo marcado; una carpeta a borrar que todavía tiene algo se deja, con un aviso.
func TestSyncRunOnlySelected(t *testing.T) {
	m, cl, local := syncRig(t, nil)
	writeLocal(t, local, "a.txt", "a", time.Time{})
	writeLocal(t, local, "b.txt", "b", time.Time{})
	cl.MkdirAll("/srv/www/viejo")
	put(t, cl, "/srv/www/viejo/x.txt", "x")

	req := syncReq(local, "up", "mirror", "time+size")
	req.Delete = true
	p := compare(t, m, req)
	if _, err := m.SyncRun("srv", "otro-plan", []string{"a.txt"}); err == nil {
		t.Fatal("SyncRun con otro plan tenía que fallar")
	}
	if _, err := m.SyncRun("srv", p.PlanID, nil); err == nil {
		t.Fatal("SyncRun sin nada marcado tenía que fallar")
	}
	if _, err := m.SyncRun("srv", p.PlanID, []string{"a.txt", "../../etc/passwd"}); err == nil {
		t.Fatal("SyncRun con una ruta que no está en el plan tenía que fallar")
	}
	id, err := m.SyncRun("srv", p.PlanID, []string{"a.txt", "viejo"})
	if err != nil {
		t.Fatal(err)
	}
	info := waitTransfer(t, m, id)
	if info.Status != StatusDone || info.Name != "Sincronización: 2 archivos" || !strings.Contains(info.Warning, "1 carpeta no se borró") {
		t.Fatalf("transferencia: %+v", info)
	}
	if get(t, cl, "/srv/www/a.txt") != "a" || remoteExists(cl, "/srv/www/b.txt") || get(t, cl, "/srv/www/viejo/x.txt") != "x" {
		t.Fatal("se ejecutó algo que no estaba marcado")
	}
	if _, err := m.SyncRun("srv", p.PlanID, []string{"b.txt"}); err == nil {
		t.Fatal("un plan se usa una sola vez")
	}
}

func TestSyncCancel(t *testing.T) {
	m, cl, local := syncRig(t, nil)
	for i := range 20 {
		writeLocal(t, local, string(rune('a'+i))+".bin", strings.Repeat("x", 256<<10), time.Time{})
	}
	p := compare(t, m, syncReq(local, "up", "sync", "size"))
	id, err := m.SyncRun("srv", p.PlanID, rels(p.Actions))
	if err != nil {
		t.Fatal(err)
	}
	m.CancelTransfer(id)
	if info := waitTransfer(t, m, id); info.Status != StatusCanceled {
		t.Fatalf("estado: %s (%s)", info.Status, info.Error)
	}
	assertNoTemps(t, cl, "/srv/www")
}

// ---- comparar por contenido ----

// fakeSha256 simula sha256sum en el servidor leyendo los archivos por SFTP (las rutas no tienen comillas).
type fakeSha256 struct {
	cl   *sftp.Client
	has  bool
	mu   sync.Mutex
	cmds []string
}

func (f *fakeSha256) run(cmd string, ch ssh.Channel) int {
	f.mu.Lock()
	f.cmds = append(f.cmds, cmd)
	cl := f.cl
	f.mu.Unlock()
	if cmd == "command -v sha256sum" {
		if f.has {
			return 0
		}
		return 1
	}
	args, ok := strings.CutPrefix(cmd, "LC_ALL=C sha256sum -- '")
	if !ok {
		return 2
	}
	status := 0
	for _, p := range strings.Split(strings.TrimSuffix(args, "'"), "' '") {
		r, err := cl.Open(p)
		if err != nil {
			io.WriteString(ch.Stderr(), "sha256sum: "+p+": No such file or directory\n")
			status = 1
			continue
		}
		h := sha256.New()
		io.Copy(h, r)
		r.Close()
		io.WriteString(ch, hex.EncodeToString(h.Sum(nil))+"  "+p+"\n")
	}
	return status
}

func TestSyncCompareByContent(t *testing.T) {
	f := &fakeSha256{has: true}
	m, cl, local := syncRig(t, f.run)
	f.mu.Lock()
	f.cl = cl
	f.mu.Unlock()
	put(t, cl, "/srv/www/igual.txt", "abc")
	put(t, cl, "/srv/www/distinto.txt", "abc")
	put(t, cl, "/srv/www/tamano.txt", "abc")
	writeLocal(t, local, "igual.txt", "abc", time.Now().Add(time.Hour))
	writeLocal(t, local, "distinto.txt", "abd", time.Now().Add(-time.Hour))
	writeLocal(t, local, "tamano.txt", "abcd", time.Time{})

	p := compare(t, m, syncReq(local, "up", "sync", "content"))
	sameStrings(t, "plan", ops(p.Actions), []string{"upload distinto.txt", "upload tamano.txt"})
	// Solo se calcula el hash de los pares de igual tamaño.
	var sha []string
	f.mu.Lock()
	for _, c := range f.cmds {
		if strings.HasPrefix(c, "LC_ALL=C sha256sum") {
			sha = append(sha, c)
		}
	}
	f.mu.Unlock()
	sameStrings(t, "comandos", sha, []string{"LC_ALL=C sha256sum -- '/srv/www/distinto.txt' '/srv/www/igual.txt'"})
	runAll(t, m, p)
	if get(t, cl, "/srv/www/distinto.txt") != "abd" || get(t, cl, "/srv/www/tamano.txt") != "abcd" {
		t.Fatal("el contenido no coincide")
	}
}

func TestSyncContentUnavailable(t *testing.T) {
	m, _, local := syncRig(t, nil) // sin exec
	if _, err := m.SyncCompare("srv", syncReq(local, "up", "sync", "content")); !errors.Is(err, errNoSha256) {
		t.Fatalf("sin exec: %v", err)
	}
	f := &fakeSha256{has: false}
	m2, _, local2 := syncRig(t, f.run)
	if _, err := m2.SyncCompare("srv", syncReq(local2, "up", "sync", "content")); !errors.Is(err, errNoSha256) {
		t.Fatalf("sin sha256sum: %v", err)
	}
	// Los otros criterios no necesitan el servidor.
	if _, err := m2.SyncCompare("srv", syncReq(local2, "up", "sync", "time+size")); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteHashesBatches(t *testing.T) {
	var mu sync.Mutex
	var cmds []string
	client := execServer(t, func(cmd string, ch ssh.Channel) int {
		mu.Lock()
		cmds = append(cmds, cmd)
		mu.Unlock()
		args, _ := strings.CutPrefix(cmd, "LC_ALL=C sha256sum -- '")
		for _, p := range strings.Split(strings.TrimSuffix(args, "'"), "' '") {
			io.WriteString(ch, strings.Repeat("0", 64)+"  "+p+"\n")
		}
		return 0
	})
	sh := &shell{c: client, off: new(atomic.Bool)}
	paths := make([]string, 450)
	for i := range paths {
		paths[i] = fmt.Sprintf("/d/f%03d", i)
	}
	got, err := remoteHashes(t.Context(), sh, paths)
	if err != nil || len(got) != len(paths) {
		t.Fatalf("hashes: %d, %v", len(got), err)
	}
	mu.Lock()
	n := len(cmds)
	mu.Unlock()
	if n != 3 {
		t.Fatalf("comandos: %d, quería 3 tandas de hasta %d", n, syncHashBatch)
	}
}

func TestSha256Command(t *testing.T) {
	got := sha256Command([]string{"/srv/a b.txt", "/srv/o'brien", "/srv/-rf", "/srv/$(reboot)"})
	want := `LC_ALL=C sha256sum -- '/srv/a b.txt' '/srv/o'\''brien' '/srv/-rf' '/srv/$(reboot)'`
	if got != want {
		t.Fatalf("\n got  %s\n want %s", got, want)
	}
}

func TestParseSha256Line(t *testing.T) {
	h := strings.Repeat("ab", 32)
	cases := []struct {
		line, hash, path string
		ok               bool
	}{
		{h + "  /srv/a.txt", h, "/srv/a.txt", true},
		{h + " */srv/a.bin", h, "/srv/a.bin", true},
		{h + "  /srv/con  espacios ", h, "/srv/con  espacios ", true},
		{`\` + h + `  /srv/salto\nde línea`, h, "/srv/salto\nde línea", true},
		{`\` + h + `  /srv/barra\\n`, h, `/srv/barra\n`, true},
		{"Bienvenido al servidor", "", "", false},
		{strings.Repeat("AB", 32) + "  /srv/a", "", "", false},
		{h + "/srv/a", "", "", false},
		{h + "  ", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		hash, p, ok := parseSha256Line(c.line)
		if ok != c.ok || hash != c.hash || p != c.path {
			t.Errorf("parseSha256Line(%q) = %q, %q, %v", c.line, hash, p, ok)
		}
	}
}

// El plan de una sesión que se cierra se olvida.
func TestSyncPlanDroppedOnClose(t *testing.T) {
	m := newBridgeManager(t)
	// Una sesión sin conexión: cerrar una con el SFTP en memoria esperaría respuestas para siempre.
	m.register(&Session{ID: "x", Kind: KindSFTP, ConnID: "c"})
	m.syncPlans["x"] = &syncPlan{id: "p"}
	m.Close("x")
	m.syncMu.Lock()
	n := len(m.syncPlans)
	m.syncMu.Unlock()
	if n != 0 {
		t.Fatalf("quedaron %d planes", n)
	}
}
