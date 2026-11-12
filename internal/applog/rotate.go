package applog

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

const (
	// maxLogSize es el tamaño a partir del cual osat.log pasa a osat.1.log.
	maxLogSize = 5 << 20
	// keepOld es la cantidad de archivos viejos que se guardan (osat.1.log … osat.3.log).
	keepOld = 3
)

// rotWriter es un io.Writer que escribe en un archivo y lo rota al pasar el límite.
// Es seguro para varias goroutines.
type rotWriter struct {
	mu   sync.Mutex
	path string // ruta de osat.log
	max  int64
	keep int
	f    *os.File
	size int64
}

func newRotWriter(path string, max int64, keep int) (*rotWriter, error) {
	w := &rotWriter{path: path, max: max, keep: keep}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

// oldName devuelve la ruta del archivo viejo número i (osat.1.log, osat.2.log…).
func oldName(path string, i int) string {
	ext := filepath.Ext(path)
	return path[:len(path)-len(ext)] + "." + strconv.Itoa(i) + ext
}

func (w *rotWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f, w.size = f, st.Size()
	return nil
}

// rotate cierra osat.log, corre los viejos un lugar (el último se pierde) y abre uno nuevo.
func (w *rotWriter) rotate() error {
	if w.f != nil {
		w.f.Close()
		w.f = nil
	}
	os.Remove(oldName(w.path, w.keep))
	for i := w.keep - 1; i >= 1; i-- {
		os.Rename(oldName(w.path, i), oldName(w.path, i+1))
	}
	os.Rename(w.path, oldName(w.path, 1))
	return w.open()
}

func (w *rotWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.size > 0 && w.size+int64(len(p)) > w.max {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
