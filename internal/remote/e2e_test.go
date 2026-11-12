//go:build e2e

// Test de integración contra un sshd real.
//
//	Uso: OSAT_E2E_HOST=127.0.0.1 OSAT_E2E_PORT=2222 OSAT_E2E_USER=kt OSAT_E2E_PASS=... \
//	     go test -tags e2e ./internal/remote/ -v
package remote

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"osat/internal/store"
)

func TestE2E(t *testing.T) {
	host := os.Getenv("OSAT_E2E_HOST")
	if host == "" {
		t.Skip("OSAT_E2E_HOST no definido")
	}
	port, _ := strconv.Atoi(os.Getenv("OSAT_E2E_PORT"))
	user, pass := os.Getenv("OSAT_E2E_USER"), os.Getenv("OSAT_E2E_PASS")

	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	st, err := store.New()
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.Save(store.ConnectionInput{Connection: store.Connection{Host: host, Port: port, User: user, AuthType: store.AuthPassword}})
	if err != nil {
		t.Fatal(err)
	}
	hk, _ := NewHostKeys(filepath.Join(st.Dir(), "known_hosts"))

	var mu sync.Mutex
	var out strings.Builder
	events := map[string]int{}
	var lastTransfer Transfer
	m := NewManager(st, hk, func(ev string, data ...interface{}) {
		mu.Lock()
		defer mu.Unlock()
		events[strings.SplitN(ev, ":", 3)[0]+":"+strings.SplitN(ev, ":", 3)[1]]++
		if strings.HasPrefix(ev, "term:data:") {
			b, _ := base64.StdEncoding.DecodeString(data[0].(string))
			out.Write(b)
		}
		if ev == "transfer:update" {
			lastTransfer = data[0].(Transfer)
		}
	})

	req := ConnectRequest{ConnectionID: c.ID, Cols: 100, Rows: 30}

	// 1) sin contraseña -> NEED_SECRET
	_, err = m.OpenTerminal(req)
	var ke *Error
	if !errors.As(err, &ke) || ke.Code != CodeNeedSecret {
		t.Fatalf("esperaba NEED_SECRET, obtuve %v", err)
	}
	// 2) host desconocido -> HOSTKEY_UNKNOWN
	req.Secret = pass
	_, err = m.OpenTerminal(req)
	if !errors.As(err, &ke) || ke.Code != CodeHostKeyUnknown {
		t.Fatalf("esperaba HOSTKEY_UNKNOWN, obtuve %v", err)
	}
	t.Logf("huella: %s", ke.Data["fingerprint"])
	if err := hk.Trust(ke.Data["host"]); err != nil {
		t.Fatal(err)
	}
	// 3) contraseña incorrecta -> AUTH_FAILED
	bad := req
	bad.Secret = "incorrecta"
	_, err = m.OpenTerminal(bad)
	if !errors.As(err, &ke) || ke.Code != CodeAuthFailed {
		t.Fatalf("esperaba AUTH_FAILED, obtuve %v", err)
	}
	// 4) terminal OK
	info, err := m.OpenTerminal(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Attach(info.ID); err != nil {
		t.Fatal(err)
	}
	_ = m.Resize(info.ID, 120, 40)
	_ = m.Write(info.ID, "echo OSAT_$((40+2)); stty size\n")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		s := out.String()
		mu.Unlock()
		if strings.Contains(s, "OSAT_42") && strings.Contains(s, "40 120") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	mu.Lock()
	if !strings.Contains(out.String(), "OSAT_42") || !strings.Contains(out.String(), "40 120") {
		t.Fatalf("salida inesperada: %q", out.String())
	}
	mu.Unlock()

	// 5) SFTP reutilizando la conexión
	sf, err := m.OpenSFTPFrom(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("home remoto: %s", sf.Home)
	local := filepath.Join(tmp, "up")
	os.MkdirAll(filepath.Join(local, "sub"), 0o755)
	big := make([]byte, 5<<20)
	for i := range big {
		big[i] = byte(i % 251)
	}
	os.WriteFile(filepath.Join(local, "sub", "big.bin"), big, 0o644)
	os.WriteFile(filepath.Join(local, "a.txt"), []byte("hola"), 0o644)

	waitTransfer := func(id string) Transfer {
		for i := 0; i < 200; i++ {
			mu.Lock()
			lt := lastTransfer
			mu.Unlock()
			if lt.ID == id && (lt.Status == StatusDone || lt.Status == StatusError) {
				return lt
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("timeout en transferencia")
		return Transfer{}
	}

	ids, err := m.Upload(sf.ID, []string{local}, sf.Home, ConflictOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	if tr := waitTransfer(ids[0]); tr.Status != StatusDone || tr.Done != tr.Total {
		t.Fatalf("upload: %+v", tr)
	}
	l, err := m.List(sf.ID, sf.Home+"/up/sub")
	if err != nil || len(l.Entries) != 1 || l.Entries[0].Size != int64(len(big)) {
		t.Fatalf("listado inesperado: %+v %v", l, err)
	}
	if txt, err := m.ReadText(sf.ID, sf.Home+"/up/a.txt"); err != nil || txt != "hola" {
		t.Fatalf("ReadText: %q %v", txt, err)
	}
	if err := m.Rename(sf.ID, sf.Home+"/up/a.txt", sf.Home+"/up/b.txt"); err != nil {
		t.Fatal(err)
	}

	dl := filepath.Join(tmp, "down")
	os.MkdirAll(dl, 0o755)
	ids, err = m.Download(sf.ID, []string{sf.Home + "/up"}, dl, ConflictOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	if tr := waitTransfer(ids[0]); tr.Status != StatusDone {
		t.Fatalf("download: %+v", tr)
	}
	got, _ := os.ReadFile(filepath.Join(dl, "up", "sub", "big.bin"))
	if len(got) != len(big) || string(got[:1000]) != string(big[:1000]) {
		t.Fatal("contenido descargado distinto")
	}
	if _, err := os.Stat(filepath.Join(dl, "up", "b.txt")); err != nil {
		t.Fatal("falta b.txt descargado")
	}
	if err := m.Remove(sf.ID, []string{sf.Home + "/up"}); err != nil {
		t.Fatal(err)
	}

	// 6) exit de la shell emite term:exit y cierra la sesión
	_ = m.Write(info.ID, "exit\n")
	time.Sleep(time.Second)
	mu.Lock()
	if events["term:exit"] != 1 {
		t.Fatalf("esperaba term:exit, eventos: %v", events)
	}
	mu.Unlock()
	// SFTP sigue vivo porque comparte el cliente (refcount)
	if _, err := m.List(sf.ID, sf.Home); err != nil {
		t.Fatalf("SFTP debería seguir vivo: %v", err)
	}
	m.CloseAll()
}
