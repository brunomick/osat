package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/pkg/sftp"

	"osat/internal/i18n"
	"osat/internal/textenc"
)

var (
	errNotText = i18n.Err("remote.theFileLooksBinaryNot")
	errExists  = i18n.Err("remote.aFileOrFolderWith")
)

type FileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	IsDir   bool   `json:"isDir"`
	IsLink  bool   `json:"isLink"`
	ModTime int64  `json:"modTime"`
}

type Listing struct {
	Path    string      `json:"path"`
	Entries []FileEntry `json:"entries"`
}

// lsMode formatea los permisos como `ls -l` (drwxr-xr-x, lrwxrwxrwx…).
// FileMode.String() de Go usa otras letras (p. ej. "L" para symlinks).
func lsMode(m os.FileMode) string {
	t := byte('-')
	switch {
	case m.IsDir():
		t = 'd'
	case m&os.ModeSymlink != 0:
		t = 'l'
	case m&os.ModeNamedPipe != 0:
		t = 'p'
	case m&os.ModeSocket != 0:
		t = 's'
	case m&os.ModeCharDevice != 0:
		t = 'c'
	case m&os.ModeDevice != 0:
		t = 'b'
	}
	return string(t) + m.Perm().String()[1:]
}

// List devuelve el contenido de un directorio remoto (carpetas primero).
func (m *Manager) List(id, dir string) (Listing, error) {
	cl, err := m.sftpOf(id)
	if err != nil {
		return Listing{}, err
	}
	l, err := listDir(cl, dir)
	if err != nil && m.connCut(id, err) {
		return l, newErr(CodeServerGone, err.Error(), nil) // el frontend lo reconoce por el código
	}
	return l, err
}

// Symlinks resueltos a la vez al listar (pkg/sftp admite pedidos concurrentes).
const maxLinkStats = 32

// listDir lista una carpeta con la menor cantidad posible de idas y vueltas
// al servidor, que es lo que pesa con latencia alta: las rutas absolutas no
// pasan por RealPath (se normalizan acá, sin seguir symlinks) y los symlinks
// se resuelven en paralelo en vez de uno por uno.
func listDir(cl *sftp.Client, dir string) (Listing, error) {
	if dir == "" {
		dir = "."
	}
	if path.IsAbs(dir) {
		dir = path.Clean(dir)
	} else if real, err := cl.RealPath(dir); err == nil {
		dir = real
	}
	infos, err := cl.ReadDir(dir)
	if err != nil {
		return Listing{}, err
	}
	out := make([]FileEntry, 0, len(infos))
	for _, fi := range infos {
		out = append(out, FileEntry{
			Name:    fi.Name(),
			Path:    path.Join(dir, fi.Name()),
			Size:    fi.Size(),
			Mode:    lsMode(fi.Mode()),
			IsDir:   fi.IsDir(),
			IsLink:  fi.Mode()&os.ModeSymlink != 0,
			ModTime: fi.ModTime().Unix(),
		})
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxLinkStats)
	for i := range out {
		if !out[i].IsLink {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(e *FileEntry) {
			defer func() { <-sem; wg.Done() }()
			if st, err := cl.Stat(e.Path); err == nil {
				e.IsDir = st.IsDir()
				if !e.IsDir {
					e.Size = st.Size()
				}
			}
		}(&out[i])
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return Listing{Path: dir, Entries: out}, nil
}

func (m *Manager) Mkdir(id, p string) error {
	cl, err := m.sftpOf(id)
	if err != nil {
		return err
	}
	return cl.MkdirAll(p)
}

func (m *Manager) Rename(id, from, to string) error {
	cl, err := m.sftpOf(id)
	if err != nil {
		return err
	}
	if err := cl.PosixRename(from, to); err != nil {
		return cl.Rename(from, to)
	}
	return nil
}

// Move mueve archivos o carpetas a dir (en el mismo servidor). No pisa nada:
// si algún destino existe devuelve un error CONFLICT y no mueve ninguno.
func (m *Manager) Move(id string, paths []string, dir string) error {
	cl, err := m.sftpOf(id)
	if err != nil {
		return err
	}
	return moveFiles(cl, paths, dir)
}

func moveFiles(cl *sftp.Client, paths []string, dir string) error {
	dir = path.Clean(dir)
	var found []string
	for _, p := range paths {
		p = path.Clean(p)
		if p == dir || strings.HasPrefix(dir, strings.TrimSuffix(p, "/")+"/") {
			return i18n.Err("remote.canTMoveQInto", path.Base(p))
		}
		if path.Dir(p) == dir {
			continue // ya está ahí
		}
		if _, err := cl.Lstat(path.Join(dir, path.Base(p))); err == nil {
			found = append(found, path.Base(p))
		}
	}
	if len(found) > 0 {
		return newErr(CodeConflict, "Ya existe un elemento con ese nombre en la carpeta de destino.",
			map[string]string{"kind": "name", "names": strings.Join(found, "\n"), "count": strconv.Itoa(len(found))})
	}
	for _, p := range paths {
		p = path.Clean(p)
		if path.Dir(p) == dir {
			continue
		}
		// Rename (no PosixRename): en SFTP v3 falla si el destino existe en
		// vez de pisarlo, por si apareció algo entre el chequeo y el movimiento.
		if err := cl.Rename(p, path.Join(dir, path.Base(p))); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Chmod(id, p string, mode uint32) error {
	cl, err := m.sftpOf(id)
	if err != nil {
		return err
	}
	return cl.Chmod(p, os.FileMode(mode))
}

// Remove borra archivos o carpetas (recursivo).
func (m *Manager) Remove(id string, paths []string) error {
	cl, err := m.sftpOf(id)
	if err != nil {
		return err
	}
	var rm func(p string) error
	rm = func(p string) error {
		fi, err := cl.Lstat(p)
		if err != nil {
			return err
		}
		if fi.IsDir() {
			children, err := cl.ReadDir(p)
			if err != nil {
				return err
			}
			for _, c := range children {
				if err := rm(path.Join(p, c.Name())); err != nil {
					return err
				}
			}
			return cl.RemoveDirectory(p)
		}
		return cl.Remove(p)
	}
	for _, p := range paths {
		if p == "/" || p == "" {
			continue
		}
		if err := rm(p); err != nil {
			return err
		}
	}
	return nil
}

const maxEditSize = 2 * 1024 * 1024

// ReadText lee un archivo de texto pequeño para el editor rápido.
func (m *Manager) ReadText(id, p string) (string, error) {
	cl, err := m.sftpOf(id)
	if err != nil {
		return "", err
	}
	s, enc, err := readTextEnc(cl, p)
	if err == nil {
		m.setTextEnc(id, p, enc)
	}
	return s, permissionErr(err, "No tenés permiso para leer este archivo.", "read")
}

// Codificación de los archivos remotos leídos para el editor: WriteText los
// guarda igual que estaban. Solo se recuerdan los que no son UTF-8 (el resto es
// el valor por defecto), así que el mapa se mantiene chico.
func textEncKey(id, p string) string { return id + "\x00" + p }

func (m *Manager) setTextEnc(id, p string, enc textenc.Encoding) {
	m.encMu.Lock()
	defer m.encMu.Unlock()
	if enc == textenc.UTF8 {
		delete(m.textEncs, textEncKey(id, p))
		return
	}
	if m.textEncs == nil {
		m.textEncs = map[string]textenc.Encoding{}
	}
	m.textEncs[textEncKey(id, p)] = enc
}

func (m *Manager) textEnc(id, p string) textenc.Encoding {
	m.encMu.Lock()
	defer m.encMu.Unlock()
	return m.textEncs[textEncKey(id, p)] // nil map: UTF8 (cero)
}

// readText lee el archivo en ~3 idas y vueltas sin importar su tamaño: Stat
// del handle, una lectura del tamaño exacto (pkg/sftp la parte en pedidos
// en paralelo) y un cierre que no se espera. io.ReadAll en cambio pide de a
// pedacitos crecientes, un viaje por cada uno.
func readText(cl *sftp.Client, p string) (string, error) {
	s, _, err := readTextEnc(cl, p)
	return s, err
}

// readTextEnc es readText más la codificación en que estaba el archivo.
func readTextEnc(cl *sftp.Client, p string) (string, textenc.Encoding, error) {
	f, err := cl.Open(p)
	if err != nil {
		return "", textenc.UTF8, err
	}
	// Es un handle de solo lectura: no hay nada que esperar del cierre.
	defer func() { go f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return "", textenc.UTF8, err
	}
	if fi.Size() > maxEditSize {
		return "", textenc.UTF8, errTooBig
	}
	// Un byte de más para notar si hay contenido después del tamaño informado
	// (creció desde el Stat, o es un archivo como /proc/* que informa 0).
	buf := make([]byte, fi.Size()+1)
	n, err := f.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", textenc.UTF8, err
	}
	b := buf[:n]
	if n == len(buf) {
		rest, err := io.ReadAll(io.NewSectionReader(f, int64(n), maxEditSize+1-int64(n)))
		if err != nil {
			return "", textenc.UTF8, err
		}
		b = append(b, rest...)
	}
	if len(b) > maxEditSize {
		return "", textenc.UTF8, errTooBig
	}
	// Wails pasa el texto como JSON, que es UTF-8: un archivo en otra
	// codificación se convierte acá (ver textenc) y WriteText lo deshace.
	s, enc, ok := textenc.Decode(b)
	if !ok {
		return "", enc, errNotText
	}
	return s, enc, nil
}

// CreateFile crea un archivo vacío y falla si ya existe (no lo trunca).
func (m *Manager) CreateFile(id, p string) error {
	cl, err := m.sftpOf(id)
	if err != nil {
		return err
	}
	if _, err := cl.Lstat(p); err == nil {
		return errExists
	}
	f, err := cl.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	return f.Close()
}

// WriteText sobrescribe un archivo remoto con el contenido dado, vía un
// temporal para no dejarlo a medias si se corta la conexión.
func (m *Manager) WriteText(id, p, content string) error {
	cl, err := m.sftpOf(id)
	if err != nil {
		return err
	}
	data, err := textenc.Encode(content, m.textEnc(id, p))
	if err != nil {
		return err
	}
	err = writeRemote(context.Background(), cl, p, bytes.NewReader(data), int64(len(data)), 0, nil)
	return permissionErr(err, "No tenés permiso para guardar este archivo.", "save")
}
