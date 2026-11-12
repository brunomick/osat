package remote

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/pkg/sftp"
)

// Escritura "segura" de archivos remotos: se escribe en un temporal en la
// misma carpeta y recién al terminar se renombra sobre el destino. Si la
// conexión se corta a mitad de camino, el archivo original queda intacto.

// tempPathFor devuelve un nombre temporal oculto junto a target.
func tempPathFor(target string) string {
	dir, base := path.Split(target)
	return dir + "." + base + ".osat-" + uuid.NewString()[:8]
}

func isPermission(err error) bool {
	var se *sftp.StatusError
	return errors.Is(err, os.ErrPermission) || (errors.As(err, &se) && se.FxCode() == sftp.ErrSSHFxPermissionDenied)
}

// resolveTarget sigue un symlink para reemplazar el archivo apuntado y no el
// link. Devuelve también la info del archivo existente (nil si no existe).
func resolveTarget(cl *sftp.Client, p string) (string, os.FileInfo) {
	// Con ReadLink y no RealPath: no todos los servidores resuelven links en realpath.
	for range 40 { // límite como SYMLOOP_MAX, por si hay un ciclo
		fi, err := cl.Lstat(p)
		if err != nil {
			return p, nil
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			return p, fi
		}
		dest, err := cl.ReadLink(p)
		if err != nil {
			return p, nil
		}
		if !path.IsAbs(dest) {
			dest = path.Join(path.Dir(p), dest)
		}
		p = dest
	}
	return p, nil
}

// replaceFile mueve tmp sobre target. posix-rename@openssh.com es atómico;
// sin esa extensión (SFTP v3 no pisa archivos) borramos y renombramos.
func replaceFile(cl *sftp.Client, tmp, target string) error {
	err := cl.PosixRename(tmp, target)
	if err == nil {
		return nil
	}
	if err = cl.Rename(tmp, target); err == nil {
		return nil
	}
	if _, serr := cl.Lstat(target); serr != nil {
		return err
	}
	if err := cl.Remove(target); err != nil {
		return err
	}
	return cl.Rename(tmp, target)
}

// keepOwnerAndMode copia permisos y dueño del archivo original al temporal.
// Devuelve false si no se pudo conservar el dueño (p. ej. editar como usuario
// común un archivo de otro): en ese caso conviene escribir en el lugar.
func keepOwnerAndMode(cl *sftp.Client, tmp string, orig os.FileInfo) bool {
	_ = cl.Chmod(tmp, orig.Mode().Perm())
	want, ok := orig.Sys().(*sftp.FileStat)
	if !ok {
		return true
	}
	got, err := cl.Lstat(tmp)
	if err != nil {
		return false
	}
	if st, ok := got.Sys().(*sftp.FileStat); ok && st.UID == want.UID && st.GID == want.GID {
		return true
	}
	return cl.Chown(tmp, int(want.UID), int(want.GID)) == nil
}

// writeRemote escribe src en p de forma segura, con progreso y cancelación.
// mode se aplica solo si el archivo no existía (si existía se conservan los
// suyos). Si no se puede usar un temporal (carpeta sin permiso de escritura,
// dueño distinto), escribe directo sobre p como último recurso.
func writeRemote(ctx context.Context, cl *sftp.Client, p string, src io.ReadSeeker, size int64, mode os.FileMode, ctr *atomic.Int64) error {
	return writeRemoteFrom(ctx, cl, p, src, size, mode, ctr, -1)
}

// writeRemoteFrom es writeRemote con off >= 0 para una subida reanudable (ver resume.go): el
// temporal tiene nombre fijo (partPathFor), se trunca en off y se sigue escribiendo desde ahí (src
// ya está en off y ctr suma off), y si la copia falla no se borra: lo decide quien llama.
func writeRemoteFrom(ctx context.Context, cl *sftp.Client, p string, src io.ReadSeeker, size int64, mode os.FileMode, ctr *atomic.Int64, off int64) error {
	if ctr == nil {
		ctr = new(atomic.Int64)
	}
	start := ctr.Load() // ctr puede ser el contador de toda una carpeta
	target, orig := resolveTarget(cl, p)
	tmp := tempPathFor(target)
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if off >= 0 {
		tmp = partPathFor(target)
		flags = os.O_WRONLY | os.O_CREATE // sin O_EXCL: puede haber quedado de un intento anterior
		if off == 0 {
			flags |= os.O_TRUNC
		}
	}

	f, err := cl.OpenFile(tmp, flags)
	if err != nil {
		if isPermission(err) && off <= 0 {
			return writeInPlace(ctx, cl, target, src, size, mode, orig == nil, ctr)
		}
		return err
	}
	if off > 0 {
		ctr.Add(off)
		if err = f.Truncate(off); err == nil {
			_, err = f.Seek(off, io.SeekStart)
		}
	}
	if err == nil {
		err = copyTo(ctx, f, src, size-max(off, 0), ctr)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if off < 0 {
			_ = cl.Remove(tmp)
		}
		return err
	}
	if orig != nil {
		if !keepOwnerAndMode(cl, tmp, orig) {
			_ = cl.Remove(tmp)
			if _, err := src.Seek(0, io.SeekStart); err != nil {
				return err
			}
			ctr.Store(start)
			return writeInPlace(ctx, cl, target, src, size, mode, false, ctr)
		}
	} else if mode != 0 {
		_ = cl.Chmod(tmp, mode)
	}
	if err := replaceFile(cl, tmp, target); err != nil {
		_ = cl.Remove(tmp)
		return err
	}
	return nil
}

func writeInPlace(ctx context.Context, cl *sftp.Client, p string, src io.Reader, size int64, mode os.FileMode, created bool, ctr *atomic.Int64) error {
	f, err := cl.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	err = copyTo(ctx, f, src, size, ctr)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil && created {
		_ = cl.Remove(p) // no dejar un archivo a medias que antes no existía
	}
	if err == nil && created && mode != 0 {
		_ = cl.Chmod(p, mode)
	}
	return err
}

func copyTo(ctx context.Context, f *sftp.File, src io.Reader, size int64, ctr *atomic.Int64) error {
	_, err := f.ReadFrom(&progressReader{ctx: ctx, r: src, n: ctr, size: size})
	return err
}
