package remote

import (
	"context"
	"errors"
	"path"
	"strings"

	"osat/internal/i18n"
)

// Formatos que se crean en el servidor. El que se usa lo decide la extensión del nombre.
type archiveKind int

const (
	kindTar   archiveKind = iota // .tar, sin comprimir
	kindTarGz                    // .tar.gz, .tgz
	kindZip                      // .zip (necesita "zip" en el servidor)
)

// kindOf dice qué formato crea un nombre (ok=false si no termina en una extensión conocida).
func kindOf(name string) (archiveKind, bool) {
	l := strings.ToLower(name)
	switch {
	case strings.HasSuffix(l, ".tar.gz"), strings.HasSuffix(l, ".tgz"):
		return kindTarGz, true
	case strings.HasSuffix(l, ".tar"):
		return kindTar, true
	case strings.HasSuffix(l, ".zip"):
		return kindZip, true
	}
	return 0, false
}

// ArchiveName da el nombre final de un archivo a partir de lo que escribió el usuario: ".tar", ".tar.gz",
// ".tgz" y ".zip" se respetan (".tar" sin comprimir); si no termina en ninguno, se le agrega ".tar.gz".
func ArchiveName(name string) string {
	name = strings.TrimSpace(name)
	if _, ok := kindOf(name); ok {
		return name
	}
	return name + ".tar.gz"
}

// archiveCommand arma el comando que crea archive (nombre dentro de dir) con los elementos names (también
// nombres dentro de dir): el archivo queda con rutas relativas ("carpeta/archivo"), sin el camino completo
// del servidor. "./" delante del archivo evita que tar tome un "a:b" por un equipo remoto.
func archiveCommand(dir, archive string, names []string) string {
	kind, _ := kindOf(archive)
	var b strings.Builder
	b.WriteString("cd " + shellQuote(dir) + " && ")
	switch kind {
	case kindZip:
		// -y: los enlaces simbólicos quedan como enlaces (como en tar). zip no entiende "--": un nombre que
		// empiece con "-" va con "./" delante.
		b.WriteString("zip -r -q -y " + shellQuote("./"+archive))
		for _, n := range names {
			if strings.HasPrefix(n, "-") {
				n = "./" + n
			}
			b.WriteString(" " + shellQuote(n))
		}
	default:
		flags := "-cf"
		if kind == kindTarGz {
			flags = "-czf"
		}
		b.WriteString("tar " + flags + " " + shellQuote("./"+archive) + " --")
		for _, n := range names {
			b.WriteString(" " + shellQuote(n))
		}
	}
	return b.String()
}

// missingTool es el error de cuando el servidor no tiene el programa (tar, zip, unzip).
func missingTool(tool string) error {
	switch tool {
	case "zip":
		return i18n.Err("remote.toolMissingZip", tool)
	case "unzip":
		return i18n.Err("remote.toolMissingUnzip", tool)
	}
	return i18n.Err("remote.toolMissing", tool)
}

// Compress crea en el servidor, con su propio tar (o zip), un archivo con los archivos y carpetas
// indicados, en la carpeta donde están (todos tienen que estar en la misma). Nunca pisa un archivo
// que ya exista. Devuelve la ruta del archivo creado.
//
// Corre el programa por la conexión SSH, así que los bytes no pasan por esta PC; si el servidor no deja
// ejecutar comandos no hay alternativa y se avisa.
func (m *Manager) Compress(id string, paths []string, archive string) (string, error) {
	s, err := m.get(id)
	if err != nil {
		return "", err
	}
	if s.sftp == nil {
		return "", i18n.Err("remote.theSessionIsNotSftp")
	}
	if len(paths) == 0 {
		return "", i18n.Err("remote.thereIsNothingToCompress")
	}
	archive = ArchiveName(archive)
	switch strings.ToLower(archive) {
	case ".tar", ".tar.gz", ".tgz", ".zip": // sin nombre
		return "", i18n.Err("remote.typeANameForThe")
	}
	if strings.ContainsAny(archive, "/\x00") {
		return "", i18n.Err("remote.invalidNameQItCan", archive)
	}
	sh := s.shellOrNil()
	if !sh.usable() {
		return "", i18n.Err("remote.theServerDoesnTAllow")
	}

	dir := path.Dir(path.Clean(paths[0]))
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		p = path.Clean(p)
		if path.Dir(p) != dir {
			return "", i18n.Err("remote.theItemsMustBeIn")
		}
		if _, err := s.sftp.Lstat(p); err != nil {
			return "", err
		}
		names = append(names, path.Base(p))
	}
	dst := path.Join(dir, archive)
	if _, err := s.sftp.Lstat(dst); err == nil {
		return "", i18n.Err("remote.qAlreadyExistsInThis", archive)
	}

	kind, _ := kindOf(archive)
	tool, okStatus := "tar", 1 // tar: 1 = "un archivo cambió mientras se leía"; el archivo quedó completo
	if kind == kindZip {
		tool, okStatus = "zip", 0
	}
	status, msg, err := sh.run(context.Background(), archiveCommand(dir, archive, names))
	switch {
	case err != nil:
		return "", err
	case status == 126 || status == 127:
		return "", missingTool(tool)
	case status > okStatus:
		_ = s.sftp.Remove(dst) // no dejar uno a medias
		if msg == "" {
			msg = i18n.T("remote.toolExited", tool, status)
		}
		return "", errors.New(msg)
	}
	if _, err := s.sftp.Lstat(dst); err != nil {
		return "", i18n.Err("remote.sFinishedFineButQ", tool, dst)
	}
	return dst, nil
}

// shellOrNil es el shell de la conexión SSH de la sesión (nil si no hay: terminales y archivos de esta PC).
func (s *Session) shellOrNil() *shell {
	if s.shared == nil {
		return nil
	}
	return &shell{c: s.shared.client, off: &s.noShell}
}
