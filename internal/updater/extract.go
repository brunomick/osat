package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"osat/internal/i18n"
)

// Relaunch termina la actualización: se llama justo antes de cerrar osat y deja corriendo
// el proceso que espera a que osat se cierre, reemplaza lo que haga falta y lo vuelve a abrir.
type Relaunch func() error

// Progress avisa el avance de la descarga.
type Progress func(done, total int64)

// writableDir dice si se puede escribir en la carpeta (para reemplazar el programa sin permisos de administrador).
func writableDir(dir string) bool {
	f, err := os.CreateTemp(dir, ".osat-write-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// executable es la ruta real de este programa (sin enlaces simbólicos).
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// extractZipFile saca del .zip el archivo name (ruta dentro del zip) y lo escribe en dest.
func extractZipFile(zipPath, name, dest string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return writeFile(dest, rc, 0o755)
	}
	return i18n.Err("update.missingInFile", name)
}

// extractZipAll descomprime todo el .zip dentro de dir, con los permisos y enlaces que trae.
func extractZipAll(zipPath, dir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		target, err := inside(dir, f.Name)
		if err != nil {
			return err
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case mode&os.ModeSymlink != 0:
			rc, err := f.Open()
			if err != nil {
				return err
			}
			link, err := io.ReadAll(io.LimitReader(rc, 4096))
			rc.Close()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(string(link), target); err != nil {
				return err
			}
		default:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			perm := mode.Perm()
			if perm == 0 {
				perm = 0o644
			}
			err = writeFile(target, rc, perm)
			rc.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// extractTarGzFile saca del .tar.gz el archivo name y lo escribe en dest.
func extractTarGzFile(tgzPath, name, dest string) error {
	f, err := os.Open(tgzPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return i18n.Err("update.missingInFile", name)
		}
		if err != nil {
			return err
		}
		if strings.TrimPrefix(h.Name, "./") == name && h.Typeflag == tar.TypeReg {
			return writeFile(dest, tr, 0o755)
		}
	}
}

// inside arma la ruta de name dentro de dir, sin dejar que salga de ahí ("../").
func inside(dir, name string) (string, error) {
	target := filepath.Join(dir, filepath.FromSlash(name))
	rel, err := filepath.Rel(dir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", i18n.Err("update.badPath", name)
	}
	return target, nil
}

func writeFile(dest string, r io.Reader, perm os.FileMode) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
