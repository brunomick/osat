package remote

import (
	"context"
	"errors"
	"path"
	"strings"

	"osat/internal/i18n"
)

// Extraer un archivo comprimido en el servidor ("Extraer aquí"). Como 7-Zip o el Explorador, para no
// ensuciar la carpeta con un montón de archivos sueltos ni pisar nada:
//   - si el archivo trae una sola carpeta (o archivo) en la raíz y no existe ya acá, se extrae directo;
//   - si no, se extrae en una carpeta nueva con el nombre del archivo ("proyecto.tar.gz" → "proyecto").
// Nunca pisa nada: lo que ya existe queda como está.

// extractExts son los archivos que se saben abrir (tar los lee comprimidos con gzip, bzip2, xz o zstd si
// el servidor tiene el programa).
var extractExts = []string{".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".tar.xz", ".txz", ".tar.zst", ".tzst", ".tar", ".zip"}

// ExtractBase dice si name es un archivo comprimido que se puede extraer y, si lo es, su nombre sin la
// extensión ("proyecto.tar.gz" → "proyecto").
func ExtractBase(name string) (base string, ok bool) {
	l := strings.ToLower(name)
	for _, e := range extractExts {
		if strings.HasSuffix(l, e) && len(name) > len(e) {
			return name[:len(name)-len(e)], true
		}
	}
	return "", false
}

func isZipName(name string) bool { return strings.HasSuffix(strings.ToLower(name), ".zip") }

// listTopCommand lista los nombres de la raíz del archivo (sin repetir, hasta 200), uno por línea:
// "a/b/c" → "a", "./a" → "a". Una ruta absoluta sale con la barra delante ("/etc"), que se rechaza.
func listTopCommand(archive string) string {
	list := "tar -tf " + shellQuote(archive)
	if isZipName(archive) {
		list = "unzip -Z1 " + shellQuote(archive)
	}
	return list + ` | sed -e 's|^\./||' -e 's|^\(/*[^/]*\).*|\1|' | sort -u | head -n 200`
}

// parseTop lee la salida de listTopCommand y rechaza archivos que escapan de la carpeta de destino.
func parseTop(out string) ([]string, error) {
	var top []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "" || l == ".": // la entrada "./" de un tar hecho con "tar cf x ."
		case strings.HasPrefix(l, "/") || l == "..":
			return nil, i18n.Err("remote.theArchiveHasPathsOutside", l)
		default:
			top = append(top, l)
		}
	}
	if len(top) == 0 {
		return nil, i18n.Err("remote.theArchiveIsEmptyOr")
	}
	return top, nil
}

// extractPlan decide dónde extraer: result es lo que queda creado (y lo que se borra si algo falla) y
// folder dice si hay que crearlo como carpeta nueva (si no, es la única entrada de la raíz del archivo).
func extractPlan(dir, base string, top []string, exists func(name string) bool) (result string, folder bool) {
	if len(top) == 1 && !exists(top[0]) {
		return path.Join(dir, top[0]), false
	}
	return path.Join(dir, CopyName(base, true, exists)), true
}

// extractCommand arma el comando que extrae archive en dest (una carpeta que existe).
// unzip -n: nunca pisa; tar: --no-same-owner para no cambiar dueños si el usuario es root.
func extractCommand(archive, dest string) string {
	if isZipName(archive) {
		return "unzip -q -n " + shellQuote(archive) + " -d " + shellQuote(dest)
	}
	return "tar -xf " + shellQuote(archive) + " -C " + shellQuote(dest) + " --no-same-owner"
}

// Extract extrae el archivo comprimido en el servidor (con su tar o unzip, sin pasar los bytes por esta
// PC) y devuelve la ruta de lo que quedó creado.
func (m *Manager) Extract(id, archive string) (string, error) {
	s, err := m.get(id)
	if err != nil {
		return "", err
	}
	if s.sftp == nil {
		return "", i18n.Err("remote.theSessionIsNotSftp")
	}
	archive = path.Clean(archive)
	base, ok := ExtractBase(path.Base(archive))
	if !ok {
		return "", i18n.Err("remote.donTKnowHowTo", path.Base(archive), strings.Join(extractExts, ", "))
	}
	sh := s.shellOrNil()
	if !sh.usable() {
		return "", i18n.Err("remote.theServerDoesnTAllow3")
	}
	if fi, err := s.sftp.Stat(archive); err != nil {
		return "", err
	} else if !fi.Mode().IsRegular() {
		return "", i18n.Err("remote.qIsNotAFile", path.Base(archive))
	}
	tool := "tar"
	if isZipName(archive) {
		tool = "unzip"
	}
	ctx := context.Background()

	out, status, msg, err := sh.runOut(ctx, listTopCommand(archive))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" { // el listado va por un pipe: el estado es el de head, el motivo está en stderr
		switch {
		case status == 126 || status == 127 || strings.Contains(msg, "not found"):
			return "", missingTool(tool)
		case msg != "":
			return "", errors.New(shorten(msg))
		}
		return "", i18n.Err("remote.theArchiveIsEmptyOr")
	}
	top, err := parseTop(out)
	if err != nil {
		return "", err
	}

	dir := path.Dir(archive)
	result, folder := extractPlan(dir, base, top, func(n string) bool {
		_, err := s.sftp.Lstat(path.Join(dir, n))
		return err == nil
	})
	dest := dir
	if folder {
		if err := s.sftp.Mkdir(result); err != nil {
			return "", err
		}
		dest = result
	}

	status, msg, err = sh.run(ctx, extractCommand(archive, dest))
	switch {
	case err != nil:
		_ = s.sftp.RemoveAll(result)
		return "", err
	case status == 126 || status == 127:
		_ = s.sftp.RemoveAll(result)
		return "", missingTool(tool)
	case status > 1: // tar y unzip: 1 = avisos (un archivo cambió, un nombre raro...); el resto se extrajo
		_ = s.sftp.RemoveAll(result) // no dejar uno a medias
		if msg == "" {
			msg = i18n.T("remote.toolExited", tool, status)
		}
		return "", errors.New(shorten(msg))
	}
	if _, err := s.sftp.Lstat(result); err != nil {
		return "", i18n.Err("remote.sFinishedFineButQ", tool, result)
	}
	return result, nil
}
