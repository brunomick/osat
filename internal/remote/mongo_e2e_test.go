package remote

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"osat/internal/store"
)

// Prueba de punta a punta con un MongoDB de verdad y el mongosh de esta PC.
// Solo corre si se le dice dónde está la base: un usuario de solo lectura
// sobre la base "ventas", que tiene la colección "pedidos" con 3 documentos,
// 2 de ellos con ciudad "Córdoba".
//
//	OSAT_TEST_MONGO_ADDR=127.0.0.1:27999 OSAT_TEST_MONGO_USER=lector OSAT_TEST_MONGO_PASSWORD=… go test ./internal/remote -run MongoE2E
func mongoE2E(t *testing.T) (addr, user, password string) {
	t.Helper()
	addr, user, password = os.Getenv("OSAT_TEST_MONGO_ADDR"), os.Getenv("OSAT_TEST_MONGO_USER"), os.Getenv("OSAT_TEST_MONGO_PASSWORD")
	if addr == "" || user == "" {
		t.Skip("OSAT_TEST_MONGO_ADDR y OSAT_TEST_MONGO_USER no están definidas")
	}
	needMongosh(t)
	return addr, user, password
}

// waitPrompt espera el prompt de mongosh en la base ("ventas> ").
func waitPrompt(t *testing.T, m *Manager, id string) {
	t.Helper()
	waitMongosh(t, "mongosh no mostró el prompt de la base", func() bool {
		_, before := mustSession(t, m, id).screen.CursorLine()
		return strings.HasSuffix(strings.TrimSpace(before), "ventas>")
	})
}

func mustSession(t *testing.T, m *Manager, id string) *Session {
	t.Helper()
	s, err := m.get(id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mongoRun(t *testing.T, m *Manager, id, command string) string {
	t.Helper()
	out, timedOut, err := m.TerminalRun(id, command, 30*time.Second)
	if err != nil || timedOut {
		t.Fatalf("%q: err=%v timedOut=%v out=%q", command, err, timedOut, out)
	}
	return out
}

func TestMongoE2E(t *testing.T) {
	addr, user, password := mongoE2E(t)
	m, st := jumpRig(t)
	c, err := st.Save(store.ConnectionInput{Connection: store.Connection{
		Name: "ventas", Protocol: store.ProtoMongo, URI: "mongodb://" + user + "@" + addr + "/ventas?authSource=admin",
	}, Secret: password})
	if err != nil {
		t.Fatal(err)
	}
	info := openMongo(t, m, ConnectRequest{ConnectionID: c.ID, Cols: 120, Rows: 40})
	waitPrompt(t, m, info.ID)
	if err := m.SetShared(info.ID, true); err != nil {
		t.Fatal(err)
	}
	if text, _ := m.TerminalText(info.ID, 0); password != "" && strings.Contains(text, password) {
		t.Fatalf("la contraseña quedó en pantalla: %q", text)
	}

	// Lo que haría Claude Code con run_command: una consulta…
	if out := mongoRun(t, m, info.ID, "db.pedidos.countDocuments({ ciudad: 'Córdoba' })"); !strings.Contains(out, "\n2") {
		t.Fatalf("countDocuments: %q", out)
	}
	// …un script de varias líneas…
	out := mongoRun(t, m, info.ID, "const total = db.pedidos.aggregate([{ $group: { _id: null, t: { $sum: '$total' } } }]).toArray()[0].t\nprint('total=' + total)")
	if !strings.Contains(out, "total=245") {
		t.Fatalf("script: %q", out)
	}
	// …y una escritura, que el servidor rechaza (usuario de solo lectura).
	if out := mongoRun(t, m, info.ID, "db.pedidos.insertOne({ cliente: 'Claude' })"); !strings.Contains(out, "not authorized") {
		t.Fatalf("la escritura debería fallar: %q", out)
	}
}

func TestMongoE2EThroughJump(t *testing.T) {
	addr, user, password := mongoE2E(t)
	m, st := jumpRig(t)
	var tunnels atomic.Int32
	bh, bp := pwServer(t, "pw-bastion", &tunnels)
	bastion, err := st.Save(store.ConnectionInput{Connection: store.Connection{Name: "bastion", Host: bh, Port: bp, User: "u", AuthType: store.AuthPassword}, Secret: "pw-bastion"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.Save(store.ConnectionInput{Connection: store.Connection{
		Name: "ventas", Protocol: store.ProtoMongo, URI: "mongodb://" + user + "@" + addr + "/ventas?authSource=admin", JumpID: bastion.ID,
	}, Secret: password})
	if err != nil {
		t.Fatal(err)
	}
	info := openMongo(t, m, ConnectRequest{ConnectionID: c.ID, Cols: 120, Rows: 40})
	waitPrompt(t, m, info.ID)
	if err := m.SetShared(info.ID, true); err != nil {
		t.Fatal(err)
	}
	if out := mongoRun(t, m, info.ID, "db.pedidos.countDocuments()"); !strings.Contains(out, "\n3") {
		t.Fatalf("countDocuments: %q", out)
	}
	if tunnels.Load() == 0 {
		t.Fatal("mongosh no pasó por el bastión")
	}
}
