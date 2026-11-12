package remote

import (
	"encoding/base64"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"osat/internal/applog"
	"osat/internal/i18n"
	"osat/internal/localterm"
	"osat/internal/vt"
)

// LocalConnID es el ConnID de las sesiones de este equipo (el frontend usa el
// mismo valor para "Este equipo").
const LocalConnID = "local"

var errNotRemote = i18n.Err("remote.thisSessionIsOnThis")

// LocalShells lista las terminales que se pueden abrir en este equipo.
func (m *Manager) LocalShells() []localterm.Shell {
	shells := localterm.Shells()
	if shells == nil {
		shells = []localterm.Shell{}
	}
	return shells
}

// OpenLocalTerminal abre una terminal de este equipo (shellID vacío = la
// predeterminada) en dir (vacío o inexistente = la carpeta personal). Usa los
// mismos eventos y llamadas que las terminales SSH.
func (m *Manager) OpenLocalTerminal(shellID, dir string, cols, rows int) (SessionInfo, error) {
	p, sh, err := localterm.Start(shellID, dir, cols, rows)
	if err != nil {
		slog.Warn("no se pudo abrir la terminal de este equipo", "shell", shellID, "err", err)
		return SessionInfo{}, err
	}
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}
	s := &Session{ID: uuid.NewString(), Kind: KindTerminal, ConnID: LocalConnID, name: sh.Name, local: p, stdin: p,
		ready: make(chan struct{}), screen: vt.New(cols, rows), title: sh.Name}
	m.register(s)

	dataEvt := "term:data:" + s.ID
	read := make(chan struct{})
	logf := openSessionLog(currentOptions().LogDir, sh.Name) // nil si el ajuste está apagado
	go func() {
		defer close(read)
		defer logf.Close()
		defer applog.Recover("terminal local: lectura")
		// Esperamos a que el frontend se suscriba para no perder el comienzo de la salida.
		select {
		case <-s.ready:
		case <-time.After(5 * time.Second):
		}
		buf := make([]byte, 32*1024)
		for {
			n, err := p.Read(buf)
			if n > 0 {
				s.screen.Write(buf[:n])
				logf.Write(buf[:n])
				m.emit(dataEvt, base64.StdEncoding.EncodeToString(buf[:n]))
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer applog.Recover("terminal local: fin del proceso")
		p.Wait()
		// Dejamos que se vacíe lo último que imprimió antes de cerrar la consola.
		time.Sleep(150 * time.Millisecond)
		p.Close()
		<-read
		m.emit("term:exit:"+s.ID, "")
		m.Close(s.ID)
	}()
	return SessionInfo{ID: s.ID, Kind: KindTerminal, ConnID: LocalConnID, Title: sh.Name}, nil
}
