package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"

	"osat/internal/i18n"
	"osat/internal/textenc"
)

// Editar archivos como root: leer y guardar con sudo en el servidor, por la misma conexión SSH.
//
// La contraseña nunca va en el mismo stdin que el contenido: si sudo no la pidiera (NOPASSWD, o una
// validación reciente todavía vigente) terminaría escrita dentro del archivo, y si fuera incorrecta
// sudo leería las líneas siguientes como nuevos intentos. Por eso todo pasa en dos pasos (ver sudoDo):
// el comando con `sudo -n` (nunca pregunta) y, si pide contraseña, validarla sola con `sudo -v` y
// repetir el comando con `sudo -n`.

// LC_ALL=C: los mensajes de sudo salen en inglés, que es lo que se interpreta en sudoError.
const (
	sudoN = "LC_ALL=C sudo -n "       // no pregunta: si hace falta contraseña, falla
	sudoS = "LC_ALL=C sudo -S -p '' " // lee la contraseña de stdin, sin mostrar el aviso

	sudoValidateCmd = sudoS + "-v"
)

var errSudoNoTimestamp = i18n.Err("remote.thisServerDoesnTRemember")

// permissionErr marca un permiso denegado por SFTP con el código PERMISSION (el frontend ofrece
// hacerlo como root); los demás errores pasan igual. kind es "read" o "save".
func permissionErr(err error, msg, kind string) error {
	if err != nil && isPermission(err) {
		return newErr(CodePermission, msg, map[string]string{"kind": kind})
	}
	return err
}

func isCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

func (s *sharedClient) sudoPassword() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sudoPass
}

func (s *sharedClient) setSudoPassword(p string) {
	s.lost() // si la conexión se corta, la borra (ver lost)
	s.mu.Lock()
	s.sudoPass = p
	s.mu.Unlock()
}

// sudoError interpreta lo que sudo escribió en stderr. nil si no es un error de sudo (es del comando).
func sudoError(status int, msg string) error {
	switch {
	case status == 127 || strings.Contains(msg, "sudo: not found") || strings.Contains(msg, "sudo: command not found"):
		return i18n.Err("remote.theServerDoesnTHave")
	case strings.Contains(msg, "a password is required"):
		return newErr(CodeSudoPassword, "sudo pide la contraseña.", nil)
	case strings.Contains(msg, "incorrect password") || strings.Contains(msg, "Sorry, try again"):
		return newErr(CodeSudoBadPassword, "Contraseña de sudo incorrecta.", nil)
	case strings.Contains(msg, "must have a tty") || strings.Contains(msg, "requiretty"):
		return i18n.Err("remote.sudoRequiresATerminalOn")
	case strings.Contains(msg, "is not in the sudoers") || strings.Contains(msg, "not allowed") || strings.Contains(msg, "may not run sudo"):
		return i18n.Err("remote.yourUserIsnTAllowed")
	}
	return nil
}

// sudoResult convierte el resultado de shell.stream en un error: nil si salió bien.
func sudoResult(status int, msg string, err error) error {
	switch {
	case err != nil:
		return err
	case status == 0:
		return nil
	}
	if e := sudoError(status, msg); e != nil {
		return e
	}
	if msg == "" {
		return i18n.Err("remote.sudoEndedWithCodeD", status)
	}
	return errors.New(shorten(msg))
}

// sudoDo corre un comando con `sudo -n` (run) por el flujo de dos pasos. password vacío = la que se
// recuerda de esta conexión (si hay). withPassword, si no es nil, corre el comando con `sudo -S` y solo
// la contraseña por stdin: es el último recurso para los servidores que no recuerdan la validación.
func sudoDo(sh *shell, sc *sharedClient, password string, run func() (int, string, error), withPassword func(pw string) (int, string, error)) error {
	err := sudoResult(run())
	if !isCode(err, CodeSudoPassword) {
		return err
	}
	if password == "" {
		if password = sc.sudoPassword(); password == "" {
			return err // el frontend la pregunta
		}
	}
	// Solo la contraseña por stdin: si es incorrecta, cuenta como un único intento fallido.
	verr := sudoResult(sh.stream(context.Background(), sudoValidateCmd, strings.NewReader(password+"\n"), nil))
	if isCode(verr, CodeSudoPassword) || isCode(verr, CodeSudoBadPassword) {
		sc.setSudoPassword("") // la recordada (si era esa) ya no sirve
		return newErr(CodeSudoBadPassword, "Contraseña de sudo incorrecta.", nil)
	}
	if verr != nil {
		return verr
	}
	sc.setSudoPassword(password)
	if err = sudoResult(run()); !isCode(err, CodeSudoPassword) {
		return err
	}
	if withPassword == nil {
		return errSudoNoTimestamp
	}
	return sudoResult(withPassword(password))
}

// sudoShell es el shell de la sesión para correr sudo.
func (m *Manager) sudoShell(id string) (*Session, *shell, error) {
	s, err := m.get(id)
	if err != nil {
		return nil, nil, err
	}
	sh := s.shellOrNil()
	if !sh.usable() {
		return nil, nil, errNoShell
	}
	return s, sh, nil
}

// La codificación de lo leído como root se recuerda aparte de la edición normal (una ruta no tiene NUL).
func sudoEncPath(p string) string { return "\x00sudo" + p }

// SudoReadText lee un archivo de texto como root (con `sudo head`), con el mismo límite y la misma
// detección de codificación que ReadText.
func (m *Manager) SudoReadText(id, p, password string) (string, error) {
	s, sh, err := m.sudoShell(id)
	if err != nil {
		return "", err
	}
	cmd := "head -c " + strconv.Itoa(maxEditSize+1) + " -- " + shellQuote(p)
	var out bytes.Buffer
	read := func(c string, stdin io.Reader) (int, string, error) {
		out.Reset()
		return sh.stream(context.Background(), c, stdin, &out)
	}
	err = sudoDo(sh, s.shared, password,
		func() (int, string, error) { return read(sudoN+cmd, nil) },
		func(pw string) (int, string, error) { return read(sudoS+cmd, strings.NewReader(pw+"\n")) })
	if err != nil {
		return "", err
	}
	if out.Len() > maxEditSize {
		return "", errTooBig
	}
	text, enc, ok := textenc.Decode(out.Bytes())
	if !ok {
		return "", errNotText
	}
	m.setTextEnc(id, sudoEncPath(p), enc)
	return text, nil
}

// SudoWriteText guarda un archivo como root con `sudo tee`, que escribe dentro del archivo existente:
// dueño y permisos no cambian. Lo único que va por stdin es el contenido.
func (m *Manager) SudoWriteText(id, p, content, password string) error {
	s, sh, err := m.sudoShell(id)
	if err != nil {
		return err
	}
	data, err := textenc.Encode(content, m.textEnc(id, sudoEncPath(p)))
	if err != nil {
		return err
	}
	cmd := sudoN + "tee -- " + shellQuote(p) + " > /dev/null"
	return sudoDo(sh, s.shared, password, func() (int, string, error) {
		return sh.stream(context.Background(), cmd, bytes.NewReader(data), nil)
	}, nil)
}
