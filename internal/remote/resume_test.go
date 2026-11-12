package remote

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"

	"osat/internal/i18n"
)

// tapConn es un cliente SFTP sobre un sistema de archivos en memoria compartido (h) que cuenta los
// bytes que pasan por la conexión y, al pasar after, llama a hit una vez (desde la goroutine que
// lee o escribe): para cortar la conexión a la mitad (cut) o frenarla mientras se cancela.
type tapConn struct {
	cl    *sftp.Client
	bytes atomic.Int64
	after int64
	hit   func()
	once  sync.Once
	pipes []io.Closer
}

func (c *tapConn) count(n int) {
	if c.bytes.Add(int64(n)) > c.after && c.after > 0 && c.hit != nil {
		c.once.Do(c.hit)
	}
}

func (c *tapConn) cut() {
	for _, p := range c.pipes {
		p.Close()
	}
}

type tapReader struct {
	c *tapConn
	r io.Reader
}

func (r tapReader) Read(b []byte) (int, error) {
	n, err := r.r.Read(b)
	r.c.count(n)
	return n, err
}

type tapWriter struct {
	c *tapConn
	w io.WriteCloser
}

func (w tapWriter) Write(b []byte) (int, error) {
	w.c.count(len(b))
	return w.w.Write(b)
}

func (w tapWriter) Close() error { return w.w.Close() }

func tapSFTP(t *testing.T, h sftp.Handlers, after int64, hit func(c *tapConn)) *tapConn {
	t.Helper()
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	srv := sftp.NewRequestServer(struct {
		io.Reader
		io.WriteCloser
	}{sr, sw}, h)
	go srv.Serve()
	c := &tapConn{after: after, pipes: []io.Closer{sw, cw, sr, cr}}
	if hit != nil {
		c.hit = func() { hit(c) }
	}
	// Las mismas opciones que startSFTP: con escrituras en paralelo, el pedazo subido puede tener huecos.
	cl, err := sftp.NewClientPipe(tapReader{c, cr}, tapWriter{c, cw},
		sftp.UseConcurrentWrites(true), sftp.UseConcurrentReads(true), sftp.MaxConcurrentRequestsPerFile(64))
	if err != nil {
		t.Fatal(err)
	}
	c.cl = cl
	t.Cleanup(func() {
		c.cut() // primero los pipes: si no, cl.Close espera respuestas para siempre
		srv.Close()
		cl.Close()
	})
	return c
}

// El servidor en memoria deja inutilizable para siempre un archivo cuya transferencia se cortó
// (TransferError): un sshd de verdad no. Estos envoltorios lo esconden.
type plainGet struct{ sftp.FileReader }

func (g plainGet) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	ra, err := g.FileReader.Fileread(r)
	return struct{ io.ReaderAt }{ra}, err
}

type plainPut struct{ sftp.FileWriter }

func (p plainPut) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	wa, err := p.FileWriter.Filewrite(r)
	return struct{ io.WriterAt }{wa}, err
}

// resumeRig: un Manager con la sesión SFTP "a" de la conexión "c", que se corta (o frena) al pasar
// after bytes, sobre el sistema de archivos h. hit nil = cortar la conexión.
func resumeRig(t *testing.T, after int64, hit func(c *tapConn)) (*Manager, sftp.Handlers, *sftp.Client) {
	t.Helper()
	m := newBridgeManager(t)
	h := sftp.InMemHandler()
	h.FileGet, h.FilePut = plainGet{h.FileGet}, plainPut{h.FilePut}
	sa := newSharedClient(forwardingServer(t))
	if hit == nil {
		hit = func(c *tapConn) {
			sa.client.Close() // la conexión SSH se perdió...
			<-sa.lost()
			c.cut() // ...y con ella el canal SFTP
		}
	}
	ta := tapSFTP(t, h, after, hit)
	m.register(&Session{ID: "a", Kind: KindSFTP, ConnID: "c", sftp: ta.cl, shared: sa})
	return m, h, tapSFTP(t, h, 0, nil).cl
}

// reconnect abre otra sesión SFTP de la misma conexión (como el explorador al reconectar).
func reconnect(t *testing.T, m *Manager, h sftp.Handlers, id string) *tapConn {
	t.Helper()
	tb := tapSFTP(t, h, 0, nil)
	m.register(&Session{ID: id, Kind: KindSFTP, ConnID: "c", sftp: tb.cl, shared: newSharedClient(forwardingServer(t))})
	return tb
}

func waitTransfer(t *testing.T, m *Manager, id string) Transfer {
	t.Helper()
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		tr := m.transfers.get(id)
		tr.mu.Lock()
		info := tr.info
		tr.mu.Unlock()
		if info.Status != StatusQueued && info.Status != StatusRunning {
			return info
		}
	}
	t.Fatal("la transferencia no terminó a tiempo")
	return Transfer{}
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func putBytes(t *testing.T, cl *sftp.Client, p string, b []byte) {
	t.Helper()
	f, err := cl.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func remoteExists(cl *sftp.Client, p string) bool { _, err := cl.Stat(p); return err == nil }

// Una descarga cortada deja el pedazo; Reintentar (por otra sesión) sigue desde ahí y el resultado es idéntico.
func TestResumeDownload(t *testing.T) {
	const size = 3 << 20
	m, _, plain := resumeRig(t, size/2, nil)
	putBytes(t, plain, "/big.bin", randomBytes(t, size))
	dir := t.TempDir()
	target := filepath.Join(dir, "big.bin")

	ids, err := m.Download("a", []string{"/big.bin"}, dir, ConflictOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	info := waitTransfer(t, m, ids[0])
	pi, perr := os.Stat(target + ".osat-part")
	if info.Status != StatusError || !info.Resumable || info.ConnID != "c" || perr != nil || pi.Size() == 0 || pi.Size() >= size || exists(target) {
		t.Fatalf("después del corte: %+v pedazo=%v %v", info, pi, perr)
	}
	if info.ResumeFrom != pi.Size() {
		t.Fatalf("sigue desde %d, el pedazo tiene %d", info.ResumeFrom, pi.Size())
	}

	// Sin otra sesión abierta de esa conexión (la "a" quedó muerta), no hay por dónde.
	if err := m.RetryTransfer(ids[0]); !errors.Is(err, errNoRetrySession) {
		t.Fatalf("sin sesión: %v", err)
	}
}

func TestResumeDownloadContinues(t *testing.T) {
	const size = 3 << 20
	m, h, plain := resumeRig(t, size/2, nil)
	src := randomBytes(t, size)
	putBytes(t, plain, "/big.bin", src)
	dir := t.TempDir()
	target := filepath.Join(dir, "big.bin")

	ids, _ := m.Download("a", []string{"/big.bin"}, dir, ConflictOverwrite)
	before := waitTransfer(t, m, ids[0])
	if !before.Resumable {
		t.Fatalf("después del corte: %+v", before)
	}
	tb := reconnect(t, m, h, "b")
	if err := m.RetryTransfer(ids[0]); err != nil {
		t.Fatal(err)
	}
	info := waitTransfer(t, m, ids[0])
	got, _ := os.ReadFile(target)
	if info.Status != StatusDone || info.Warning != "" || info.SessionID != "b" || !bytes.Equal(got, src) || exists(target+".osat-part") {
		t.Fatalf("reintento: %+v iguales=%v", info, bytes.Equal(got, src))
	}
	if n := tb.bytes.Load(); n > size-before.ResumeFrom+size/10 {
		t.Fatalf("bajó %d bytes: no siguió desde el pedazo (%d)", n, before.ResumeFrom)
	}
}

// Si el origen cambió desde que empezó, se baja de cero con un aviso.
func TestResumeDownloadSourceChanged(t *testing.T) {
	const size = 3 << 20
	m, h, plain := resumeRig(t, size/2, nil)
	putBytes(t, plain, "/big.bin", randomBytes(t, size))
	dir := t.TempDir()
	ids, _ := m.Download("a", []string{"/big.bin"}, dir, ConflictOverwrite)
	if info := waitTransfer(t, m, ids[0]); !info.Resumable {
		t.Fatalf("después del corte: %+v", info)
	}
	src := randomBytes(t, size+1)
	putBytes(t, plain, "/big.bin", src)
	reconnect(t, m, h, "b")
	if err := m.RetryTransfer(ids[0]); err != nil {
		t.Fatal(err)
	}
	info := waitTransfer(t, m, ids[0])
	got, _ := os.ReadFile(filepath.Join(dir, "big.bin"))
	if info.Status != StatusDone || info.Warning != i18n.T(warnChangedKey) || !bytes.Equal(got, src) {
		t.Fatalf("reintento: %+v iguales=%v", info, bytes.Equal(got, src))
	}
}

// Si el final del pedazo no coincide con el origen, también se baja de cero con un aviso.
func TestResumeDownloadBadPart(t *testing.T) {
	const size = 3 << 20
	m, h, plain := resumeRig(t, size/2, nil)
	src := randomBytes(t, size)
	putBytes(t, plain, "/big.bin", src)
	dir := t.TempDir()
	target := filepath.Join(dir, "big.bin")
	ids, _ := m.Download("a", []string{"/big.bin"}, dir, ConflictOverwrite)
	if info := waitTransfer(t, m, ids[0]); !info.Resumable {
		t.Fatalf("después del corte: %+v", info)
	}
	f, _ := os.OpenFile(target+".osat-part", os.O_WRONLY|os.O_APPEND, 0)
	f.Write([]byte("basura"))
	f.Close()
	reconnect(t, m, h, "b")
	if err := m.RetryTransfer(ids[0]); err != nil {
		t.Fatal(err)
	}
	info := waitTransfer(t, m, ids[0])
	got, _ := os.ReadFile(target)
	if info.Status != StatusDone || info.Warning != i18n.T(warnMismatchKey) || !bytes.Equal(got, src) {
		t.Fatalf("reintento: %+v iguales=%v", info, bytes.Equal(got, src))
	}
}

// Cancelar no deja pedazo.
func TestResumeDownloadCancel(t *testing.T) {
	const size = 3 << 20
	reached, release := make(chan struct{}), make(chan struct{})
	m, _, plain := resumeRig(t, size/2, func(*tapConn) { close(reached); <-release })
	putBytes(t, plain, "/big.bin", randomBytes(t, size))
	dir := t.TempDir()
	ids, _ := m.Download("a", []string{"/big.bin"}, dir, ConflictOverwrite)
	<-reached
	m.CancelTransfer(ids[0])
	close(release)
	info := waitTransfer(t, m, ids[0])
	if info.Status != StatusCanceled || info.Resumable || exists(filepath.Join(dir, "big.bin.osat-part")) || exists(filepath.Join(dir, "big.bin")) {
		t.Fatalf("cancelada: %+v", info)
	}
}

// Una subida cortada deja el pedazo remoto; Reintentar sigue desde ahí y el resultado es idéntico.
func TestResumeUpload(t *testing.T) {
	const size = 8 << 20
	m, h, plain := resumeRig(t, size-size/16, nil)
	plain.Mkdir("/up")
	local := filepath.Join(t.TempDir(), "big.bin")
	src := randomBytes(t, size)
	os.WriteFile(local, src, 0o644)

	ids, err := m.Upload("a", []string{local}, "/up", ConflictOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	before := waitTransfer(t, m, ids[0])
	if before.Status != StatusError || !before.Resumable || !remoteExists(plain, "/up/.big.bin.osat-part") || remoteExists(plain, "/up/big.bin") {
		t.Fatalf("después del corte: %+v", before)
	}
	if err := m.RetryTransfer(ids[0]); !errors.Is(err, errNoRetrySession) {
		t.Fatalf("sin sesión: %v", err)
	}

	tb := reconnect(t, m, h, "b")
	if err := m.RetryTransfer(ids[0]); err != nil {
		t.Fatal(err)
	}
	info := waitTransfer(t, m, ids[0])
	got := []byte(get(t, plain, "/up/big.bin"))
	if info.Status != StatusDone || info.Warning != "" || !bytes.Equal(got, src) || remoteExists(plain, "/up/.big.bin.osat-part") {
		t.Fatalf("reintento: %+v iguales=%v", info, bytes.Equal(got, src))
	}
	// El pedazo queda en ~7 MB y se sigue 4 MB antes: tiene que pasar bastante menos que el archivo entero.
	if n := tb.bytes.Load(); n > size-(1<<20) {
		t.Fatalf("subió %d bytes de %d: no siguió desde el pedazo", n, size)
	}
	assertNoTemps(t, plain, "/up")
}

func TestResumeUploadSourceChanged(t *testing.T) {
	const size = 6 << 20
	m, h, plain := resumeRig(t, size-size/8, nil)
	plain.Mkdir("/up")
	local := filepath.Join(t.TempDir(), "big.bin")
	os.WriteFile(local, randomBytes(t, size), 0o644)
	ids, _ := m.Upload("a", []string{local}, "/up", ConflictOverwrite)
	if info := waitTransfer(t, m, ids[0]); !info.Resumable {
		t.Fatalf("después del corte: %+v", info)
	}
	src := randomBytes(t, size+1)
	os.WriteFile(local, src, 0o644)
	reconnect(t, m, h, "b")
	if err := m.RetryTransfer(ids[0]); err != nil {
		t.Fatal(err)
	}
	info := waitTransfer(t, m, ids[0])
	if got := []byte(get(t, plain, "/up/big.bin")); info.Status != StatusDone || info.Warning != i18n.T(warnChangedKey) || !bytes.Equal(got, src) {
		t.Fatalf("reintento: %+v iguales=%v", info, bytes.Equal(got, src))
	}
	assertNoTemps(t, plain, "/up")
}

func TestResumeUploadCancel(t *testing.T) {
	const size = 6 << 20
	reached, release := make(chan struct{}), make(chan struct{})
	m, _, plain := resumeRig(t, size/2, func(*tapConn) { close(reached); <-release })
	plain.Mkdir("/up")
	local := filepath.Join(t.TempDir(), "big.bin")
	os.WriteFile(local, randomBytes(t, size), 0o644)
	ids, _ := m.Upload("a", []string{local}, "/up", ConflictOverwrite)
	<-reached
	m.CancelTransfer(ids[0])
	close(release)
	info := waitTransfer(t, m, ids[0])
	if info.Status != StatusCanceled || info.Resumable {
		t.Fatalf("cancelada: %+v", info)
	}
	assertNoTemps(t, plain, "/up")
	if remoteExists(plain, "/up/big.bin") {
		t.Fatal("quedó el destino a medias")
	}
}

// Un archivo de 1 MB o menos se reintenta, pero de cero (no deja pedazo).
func TestRetrySmallFileFromScratch(t *testing.T) {
	const size = 512 << 10
	m, h, plain := resumeRig(t, size/2, nil)
	src := randomBytes(t, size)
	putBytes(t, plain, "/small.bin", src)
	dir := t.TempDir()
	ids, _ := m.Download("a", []string{"/small.bin"}, dir, ConflictOverwrite)
	info := waitTransfer(t, m, ids[0])
	if info.Status != StatusError || info.Resumable || exists(filepath.Join(dir, "small.bin.osat-part")) {
		t.Fatalf("después del corte: %+v", info)
	}
	reconnect(t, m, h, "b")
	if err := m.RetryTransfer(ids[0]); err != nil {
		t.Fatal(err)
	}
	info = waitTransfer(t, m, ids[0])
	got, _ := os.ReadFile(filepath.Join(dir, "small.bin"))
	if info.Status != StatusDone || info.Warning != "" || !bytes.Equal(got, src) {
		t.Fatalf("reintento: %+v", info)
	}
	if err := m.RetryTransfer(ids[0]); err == nil {
		t.Fatal("una transferencia terminada no se reintenta")
	}
}

// Reintentar una carpeta usa la política de conflictos del intento original. Con "omitir" reemplaza
// solo lo que escribió la propia transferencia: un archivo que apareció en el destino después del
// corte queda intacto y el resto se copia. Con "Reemplazar", como siempre, se pisa.
func TestRetryFolderKeepsForeignFiles(t *testing.T) {
	const fileSize = 512 << 10
	names := []string{"a.bin", "b.bin", "c.bin"}
	foreign := []byte("lo escribió otro")

	for _, tc := range []struct {
		dir, policy string
		keep        bool
	}{
		{DirDown, ConflictSkip, true},
		{DirDown, ConflictAsk, true}, // "preguntar" sin conflictos cuenta como omitir
		{DirDown, ConflictOverwrite, false},
		{DirUp, ConflictSkip, true},
		{DirUp, ConflictOverwrite, false},
	} {
		t.Run(tc.dir+"-"+tc.policy, func(t *testing.T) {
			m, h, plain := resumeRig(t, fileSize+fileSize/2, nil)
			src := map[string][]byte{}
			for _, n := range names {
				src[n] = randomBytes(t, fileSize)
			}
			localRoot := t.TempDir()

			// La carpeta de destino es nueva; at, has, read y write miran adentro de ella.
			var at func(n string) string
			var has func(p string) bool
			var read func(p string) []byte
			var write func(p string, b []byte)
			var ids []string
			var err error
			if tc.dir == DirDown {
				plain.Mkdir("/dir")
				for n, b := range src {
					putBytes(t, plain, "/dir/"+n, b)
				}
				at = func(n string) string { return filepath.Join(localRoot, "dir", n) }
				has = exists
				read = func(p string) []byte { b, _ := os.ReadFile(p); return b }
				write = func(p string, b []byte) { os.WriteFile(p, b, 0o644) }
				ids, err = m.Download("a", []string{"/dir"}, localRoot, tc.policy)
			} else {
				os.Mkdir(filepath.Join(localRoot, "dir"), 0o755)
				for n, b := range src {
					os.WriteFile(filepath.Join(localRoot, "dir", n), b, 0o644)
				}
				plain.Mkdir("/up")
				at = func(n string) string { return "/up/dir/" + n }
				has = func(p string) bool { return remoteExists(plain, p) }
				read = func(p string) []byte { return []byte(get(t, plain, p)) }
				write = func(p string, b []byte) { putBytes(t, plain, p, b) }
				ids, err = m.Upload("a", []string{filepath.Join(localRoot, "dir")}, "/up", tc.policy)
			}
			if err != nil {
				t.Fatal(err)
			}

			if info := waitTransfer(t, m, ids[0]); info.Status != StatusError || info.Resumable {
				t.Fatalf("después del corte: %+v", info)
			}
			// Uno quedó copiado entero por la transferencia; en el lugar de otro que todavía no estaba aparece uno ajeno.
			var copied, other string
			for _, n := range names {
				if has(at(n)) {
					copied = n
				} else if other == "" {
					other = n
				}
			}
			if copied == "" || other == "" {
				t.Fatalf("el corte no quedó a mitad de la carpeta (copiado=%q pendiente=%q)", copied, other)
			}
			write(at(other), foreign)

			reconnect(t, m, h, "b")
			if err := m.RetryTransfer(ids[0]); err != nil {
				t.Fatal(err)
			}
			info := waitTransfer(t, m, ids[0])
			if info.Status != StatusDone {
				t.Fatalf("reintento: %+v", info)
			}
			for _, n := range names {
				want := src[n]
				if n == other && tc.keep {
					want = foreign
				}
				if got := read(at(n)); !bytes.Equal(got, want) {
					t.Errorf("%s: %d bytes, quería %d (ajeno que se conserva: %v)", n, len(got), len(want), n == other && tc.keep)
				}
			}
			wantWarn := ""
			if tc.keep {
				wantWarn = keptWarning(1)
			}
			if info.Warning != wantWarn {
				t.Errorf("aviso: %q", info.Warning)
			}
			// En las subidas no se mira: el temporal del archivo que se estaba subiendo al cortarse queda
			// en el servidor (con la conexión caída no se puede borrar), como en cualquier subida cortada.
			if tc.dir == DirDown {
				if parts, _ := filepath.Glob(at("*.osat-part")); len(parts) > 0 {
					t.Errorf("quedaron pedazos: %v", parts)
				}
			}
		})
	}
}
