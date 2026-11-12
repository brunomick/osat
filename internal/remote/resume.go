package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/pkg/sftp"

	"osat/internal/i18n"
)

// Reanudar transferencias. Una subida o descarga de un solo archivo de más de resumeMin escribe en
// un temporal con nombre fijo (el pedazo): si se corta la conexión, el pedazo queda y Reintentar
// sigue desde ahí, pero solo si es seguro: el origen no cambió de tamaño ni de fecha desde que
// empezó y el final del pedazo coincide con el origen. Si no, empieza de cero con un aviso. Si el
// usuario cancela (o cualquier otro error), el pedazo se borra como siempre (ver transfers.launch).
const (
	resumeMin   = 1 << 20  // archivos más chicos (y carpetas) se reintentan de cero
	resumeCheck = 64 << 10 // cuánto del final del pedazo se compara con el origen
	// Las subidas escriben con muchos pedidos en paralelo (UseConcurrentWrites): después de un corte
	// el pedazo remoto puede tener huecos antes del final. Se descarta esto del final, más que lo que
	// puede haber en vuelo (resumeInFlight).
	resumeBack = 4 << 20
	// Lo que una subida puede tener leído del archivo local pero todavía sin escribir en el servidor
	// (MaxConcurrentRequestsPerFile(64) × 32 KB).
	resumeInFlight = 2 << 20
)

const (
	warnChangedKey  = "remote.warnChanged"
	warnNoPartKey   = "remote.warnNoPart"
	warnMismatchKey = "remote.warnMismatch"
)

var errNoRetrySession = i18n.Err("remote.openThatConnectionSExplorer")

// partPathFor es el pedazo de una subida reanudable: oculto, junto al destino.
func partPathFor(target string) string {
	dir, base := path.Split(target)
	return dir + "." + base + ".osat-part"
}

func (tr *transfer) setDrop(f func()) {
	tr.mu.Lock()
	tr.drop = f
	tr.mu.Unlock()
}

func (tr *transfer) warn(msg string) {
	tr.mu.Lock()
	tr.info.Warning = msg
	tr.mu.Unlock()
}

func changed(now, orig os.FileInfo) bool {
	return now.Size() != orig.Size() || !now.ModTime().Equal(orig.ModTime())
}

// sameTail compara los resumeCheck bytes anteriores a off en el pedazo y en el origen.
func sameTail(part, src io.ReaderAt, off int64) bool {
	n := min(int64(resumeCheck), off)
	a, b := make([]byte, n), make([]byte, n)
	if _, err := io.ReadFull(io.NewSectionReader(part, off-n, n), a); err != nil {
		return false
	}
	if _, err := io.ReadFull(io.NewSectionReader(src, off-n, n), b); err != nil {
		return false
	}
	return bytes.Equal(a, b)
}

// downloadPart es downloadFile con el pedazo (local + ".osat-part") reanudable. orig: el archivo
// remoto al empezar. Si falla no borra el pedazo (lo decide transfers.launch con tr.drop).
func downloadPart(ctx context.Context, cl *sftp.Client, remote, local string, orig os.FileInfo, tr *transfer, resume bool) error {
	tmp := local + ".osat-part"
	tr.setDrop(func() { os.Remove(tmp) })
	src, err := cl.Open(remote)
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return err
	}
	var off int64
	if resume {
		off = downloadOffset(src, orig, tmp, tr)
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if off > 0 {
		flags = os.O_WRONLY | os.O_APPEND // las descargas escriben en orden: el pedazo no tiene huecos
	}
	dst, err := os.OpenFile(tmp, flags, 0o666)
	if err != nil {
		return err
	}
	tr.done.Store(off)
	if _, err = src.Seek(off, io.SeekStart); err == nil {
		_, err = src.WriteTo(&progressWriter{ctx: ctx, w: dst, n: &tr.done})
	}
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, local)
	}
	return err
}

// downloadOffset dice desde dónde seguir una descarga cortada (0 = de cero, con un aviso si no es seguro).
func downloadOffset(src *sftp.File, orig os.FileInfo, tmp string, tr *transfer) int64 {
	st, err := src.Stat()
	if err != nil || changed(st, orig) {
		tr.warn(i18n.T(warnChangedKey))
		return 0
	}
	pi, err := os.Stat(tmp)
	if err != nil {
		tr.warn(i18n.T(warnNoPartKey))
		return 0
	}
	if pi.Size() > st.Size() {
		tr.warn(i18n.T(warnMismatchKey))
		return 0
	}
	part, err := os.Open(tmp)
	if err != nil {
		tr.warn(i18n.T(warnNoPartKey))
		return 0
	}
	defer part.Close()
	if !sameTail(part, src, pi.Size()) {
		tr.warn(i18n.T(warnMismatchKey))
		return 0
	}
	return pi.Size()
}

// uploadPart es uploadFile con el pedazo remoto (partPathFor) reanudable. orig: el archivo local al
// empezar. Si falla no borra el pedazo (lo decide transfers.launch con tr.drop).
func uploadPart(ctx context.Context, cl *sftp.Client, local, remote string, orig os.FileInfo, tr *transfer, resume bool) error {
	target, _ := resolveTarget(cl, remote)
	part := partPathFor(target)
	tr.setDrop(func() { _ = cl.Remove(part) })
	src, err := os.Open(local)
	if err != nil {
		return err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return err
	}
	mode := st.Mode().Perm()
	if runtime.GOOS == "windows" { // como en uploadFile
		mode = 0
	}
	var off int64
	if resume {
		off = uploadOffset(cl, src, st, orig, part, tr)
	}
	if _, err := src.Seek(off, io.SeekStart); err != nil {
		return err
	}
	return writeRemoteFrom(ctx, cl, remote, src, st.Size(), mode, &tr.done, off)
}

// uploadOffset dice desde dónde seguir una subida cortada (0 = de cero, con un aviso si no es
// seguro): resumeBack antes del final del pedazo, por los huecos que pueden dejar las escrituras en
// paralelo (writeRemoteFrom lo trunca ahí).
func uploadOffset(cl *sftp.Client, src *os.File, st, orig os.FileInfo, part string, tr *transfer) int64 {
	if changed(st, orig) {
		tr.warn(i18n.T(warnChangedKey))
		return 0
	}
	pi, err := cl.Stat(part)
	if err != nil {
		tr.warn(i18n.T(warnNoPartKey))
		return 0
	}
	if pi.Size() > st.Size() {
		tr.warn(i18n.T(warnMismatchKey))
		return 0
	}
	off := pi.Size() - resumeBack
	if off <= 0 {
		return 0 // quedó poco: se sube de cero
	}
	f, err := cl.Open(part)
	if err != nil {
		tr.warn(i18n.T(warnNoPartKey))
		return 0
	}
	defer f.Close()
	if !sameTail(f, src, off) {
		tr.warn(i18n.T(warnMismatchKey))
		return 0
	}
	return off
}

// connCut dice si err es porque se cortó la conexión de la sesión (y no un error del archivo).
func (m *Manager) connCut(sessionID string, err error) bool {
	if errors.Is(err, sftp.ErrSSHFxConnectionLost) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	s, gerr := m.get(sessionID)
	return gerr == nil && s.shared != nil && s.shared.isLost()
}

func (s *sharedClient) isLost() bool {
	select {
	case <-s.lost():
		return true
	default:
		return false
	}
}

// liveSFTP es una sesión SFTP abierta de la conexión connID cuya conexión no se perdió (al
// reconectar, la sesión vieja sigue registrada con un cliente muerto).
func (m *Manager) liveSFTP(connID string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.Kind == KindSFTP && s.sftp != nil && s.ConnID == connID && (s.shared == nil || !s.shared.isLost()) {
			return s
		}
	}
	return nil
}

// RetryTransfer vuelve a correr una transferencia que falló, en la misma fila del panel, por una
// sesión SFTP abierta de la misma conexión. Un archivo grande cortado sigue desde el pedazo si es
// seguro; el resto empieza de cero.
func (m *Manager) RetryTransfer(id string) error {
	tr := m.transfers.get(id)
	if tr == nil {
		return i18n.Err("remote.theTransferIsNoLonger")
	}
	tr.mu.Lock()
	connID := tr.info.ConnID
	tr.mu.Unlock()
	s := m.liveSFTP(connID)
	if s == nil {
		return errNoRetrySession
	}
	tr.mu.Lock()
	if tr.info.Status != StatusError {
		tr.mu.Unlock()
		return i18n.Err("remote.onlyAFailedTransferCan")
	}
	resume := tr.info.Resumable
	tr.info.SessionID = s.ID // cerrar esa pestaña la cancela
	tr.info.Status = StatusQueued
	tr.info.Error, tr.info.Warning = "", ""
	tr.info.Resumable, tr.info.ResumeFrom = false, 0
	tr.mu.Unlock()
	tr.done.Store(0)
	m.transfers.launch(tr, s.sftp, true, resume)
	return nil
}

// owned son los archivos del destino que escribió la transferencia, con la política "omitir" (ver
// transfer.policy). Al reintentar, lo que no está acá y ya existe en el destino es ajeno (apareció
// después del corte) y se deja como estaba; lo propio se reemplaza. Los pedazos y temporales
// (.osat-part, .osat-xxxx) tienen nombre propio y no cuentan. nil = "Reemplazar": se escribe todo.
type owned struct {
	mu    sync.Mutex
	paths map[string]bool
	retry bool // el intento en curso es un reintento
	kept  int  // archivos ajenos que dejó como estaban
}

func (o *owned) begin(retry bool) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.retry, o.kept = retry, 0
	o.mu.Unlock()
}

// may dice si se puede escribir p. exists solo se consulta en un reintento y si p no es propio.
func (o *owned) may(p string, exists func() bool) bool {
	if o == nil {
		return true
	}
	o.mu.Lock()
	check := o.retry && !o.paths[p]
	o.mu.Unlock()
	if !check || !exists() {
		return true
	}
	o.mu.Lock()
	o.kept++
	o.mu.Unlock()
	return false
}

// wrote marca p como escrito entero por la transferencia.
func (o *owned) wrote(p string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.paths[p] = true
	o.mu.Unlock()
}

func (o *owned) keptCount() int {
	if o == nil {
		return 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.kept
}

func keptWarning(n int) string {
	if n == 1 {
		return i18n.T("remote.keptOne")
	}
	return i18n.T("remote.keptMany", n)
}
