package remote

import (
	"bytes"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"osat/internal/store"
	"osat/internal/vt"
)

// La contraseña se escribe una sola vez, cuando mongosh la pide.
func TestMongoPasswordAnswer(t *testing.T) {
	screen := vt.New(80, 24)
	var typed bytes.Buffer
	a := newPasswordAnswer("s3cret", time.Now())

	screen.Write([]byte("Current Mongosh Log ID: 123\r\n"))
	a.check(screen, &typed)
	if typed.Len() != 0 {
		t.Fatalf("escribió antes de que la pidieran: %q", typed.String())
	}
	screen.Write([]byte("Enter password: "))
	a.check(screen, &typed)
	if typed.String() != "s3cret\r" {
		t.Fatalf("escribió %q", typed.String())
	}
	// Si vuelve a aparecer (otra cosa la imprime), no se repite.
	screen.Write([]byte("\r\nEnter password: "))
	a.check(screen, &typed)
	if typed.String() != "s3cret\r" {
		t.Fatalf("la escribió dos veces: %q", typed.String())
	}

	// Pasado el plazo, ya no se escribe.
	late := newPasswordAnswer("s3cret", time.Now().Add(-mongoPasswordWait-time.Second))
	typed.Reset()
	late.check(screen, &typed)
	if typed.Len() != 0 {
		t.Fatalf("la escribió fuera de tiempo: %q", typed.String())
	}
}

// Un comando de una línea va tal cual; uno de varias se carga con load().
func TestMongoScript(t *testing.T) {
	s := &Session{scripts: t.TempDir()}
	line, remove, err := mongoScript(s, "db.ventas.countDocuments()")
	if err != nil || line != "db.ventas.countDocuments()" {
		t.Fatalf("%q %v", line, err)
	}
	remove()

	script := "const n = db.ventas.countDocuments()\nprint(`hay ${n}`)\n"
	line, remove, err = mongoScript(s, script)
	if err != nil || !strings.HasPrefix(line, `load("`) || !strings.HasSuffix(line, `")`) || strings.ContainsAny(line, "\r\n") {
		t.Fatalf("%q %v", line, err)
	}
	path, err := strconv.Unquote(strings.TrimSuffix(strings.TrimPrefix(line, "load("), ")"))
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != script {
		t.Fatalf("archivo: %q %v", b, err)
	}
	remove()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("no se borró el script: %v", err)
	}
}

// fakeMongo acepta conexiones y no contesta: alcanza para ver que mongosh llegó.
func fakeMongo(t *testing.T) (int, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var n atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			go func() { // se cierra cuando mongosh la suelta
				buf := make([]byte, 4096)
				for {
					if _, err := c.Read(buf); err != nil {
						c.Close()
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, &n
}

func needMongosh(t *testing.T) {
	t.Helper()
	if _, err := FindMongosh(); err != nil {
		t.Skip("no hay mongosh en esta PC")
	}
}

// openMongo abre la conexión confiando en las huellas SSH nuevas (de los saltos).
func openMongo(t *testing.T, m *Manager, req ConnectRequest) SessionInfo {
	t.Helper()
	for i := 0; i < 5; i++ {
		info, err := m.OpenMongoShell(req)
		var ke *Error
		if errors.As(err, &ke) && ke.Code == CodeHostKeyUnknown {
			if err := m.hostKeys.Trust(ke.Data["host"], ke.Data["fingerprint"]); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { m.Close(info.ID) })
		if err := m.Attach(info.ID); err != nil {
			t.Fatal(err)
		}
		return info
	}
	t.Fatal("demasiados intentos")
	return SessionInfo{}
}

// waitMongosh espera más que waitFor: mongosh tarda unos segundos en arrancar.
func waitMongosh(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); !ok(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
	}
}

// mongosh arranca sin la contraseña en la línea de comandos, osat la escribe
// cuando la pide y llega al servidor. El puente la muestra como "mongo" y no
// le acepta comandos "de solo lectura" de shell.
func TestOpenMongoShell(t *testing.T) {
	needMongosh(t)
	m, st := jumpRig(t)
	port, conns := fakeMongo(t)
	c, err := st.Save(store.ConnectionInput{Connection: store.Connection{
		Name: "ventas", Protocol: store.ProtoMongo, URI: "mongodb://alice@127.0.0.1:" + strconv.Itoa(port) + "/?serverSelectionTimeoutMS=3000",
	}, Secret: "s3cret-de-prueba"})
	if err != nil {
		t.Fatal(err)
	}
	info := openMongo(t, m, ConnectRequest{ConnectionID: c.ID, Cols: 100, Rows: 30})
	waitMongosh(t, "mongosh no llegó al servidor (¿no se escribió la contraseña?)", func() bool { return conns.Load() > 0 })

	s, err := m.get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	text := s.screen.Text(0)
	if !strings.Contains(text, "Enter password:") {
		t.Fatalf("mongosh no pidió la contraseña: %q", text)
	}
	if strings.Contains(text, "s3cret-de-prueba") {
		t.Fatalf("la contraseña quedó en pantalla: %q", text)
	}

	if err := m.SetShared(info.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := m.SharedTerminals(); len(got) != 1 || got[0].Kind != "mongo" || got[0].Title != "ventas" {
		t.Fatalf("SharedTerminals = %+v", got)
	}
	if err := m.TerminalReady(info.ID); err == nil || !strings.Contains(err.Error(), "mongosh") {
		t.Fatalf("TerminalReady = %v", err)
	}
}

// Con un salto, mongosh sale por el servidor SSH (proxy SOCKS de osat).
func TestOpenMongoShellThroughJump(t *testing.T) {
	needMongosh(t)
	m, st := jumpRig(t)
	var tunnels atomic.Int32
	bh, bp := pwServer(t, "pw-bastion", &tunnels)
	port, conns := fakeMongo(t)
	bastion, err := st.Save(store.ConnectionInput{Connection: store.Connection{Name: "bastion", Host: bh, Port: bp, User: "u", AuthType: store.AuthPassword}, Secret: "pw-bastion"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.Save(store.ConnectionInput{Connection: store.Connection{
		Name: "interna", Protocol: store.ProtoMongo, URI: "mongodb://127.0.0.1:" + strconv.Itoa(port) + "/?serverSelectionTimeoutMS=3000", JumpID: bastion.ID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	info := openMongo(t, m, ConnectRequest{ConnectionID: c.ID})
	waitMongosh(t, "mongosh no llegó al servidor por el salto", func() bool { return conns.Load() > 0 && tunnels.Load() > 0 })

	// Al cerrar la pestaña se cierran el proxy y la conexión SSH.
	s, _ := m.get(info.ID)
	m.Close(info.ID)
	if _, err := os.Stat(s.scripts); !os.IsNotExist(err) {
		t.Fatalf("no se borró la carpeta de scripts: %v", err)
	}
}

func TestOpenMongoShellWithoutMongosh(t *testing.T) {
	m, st := jumpRig(t)
	prev := FindMongosh
	FindMongosh = func() (string, error) { return "", errors.New("no hay") }
	t.Cleanup(func() { FindMongosh = prev })
	c, err := st.Save(store.ConnectionInput{Connection: store.Connection{Protocol: store.ProtoMongo, URI: "mongodb://localhost"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.OpenMongoShell(ConnectRequest{ConnectionID: c.ID}); err == nil {
		t.Fatal("sin mongosh debería fallar")
	}
	ssh, err := st.Save(store.ConnectionInput{Connection: store.Connection{Name: "s", Host: "h", User: "u", AuthType: store.AuthAgent}})
	if err != nil {
		t.Fatal(err)
	}
	FindMongosh = prev
	if _, err := m.OpenMongoShell(ConnectRequest{ConnectionID: ssh.ID}); err == nil {
		t.Fatal("una conexión SSH no se abre con mongosh")
	}
}
