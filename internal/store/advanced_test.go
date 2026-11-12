package store

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestAdvancedFieldsValidation(t *testing.T) {
	ssh := func(cmd string, env ...string) Connection {
		return Connection{Host: "h", User: "u", ForwardAgent: true, InitialCommand: cmd, Env: env}
	}
	okCases := []Connection{
		ssh(""),
		ssh("cd /srv && ls", "LANG=es_AR.UTF-8", "_X1=", "A=b=c", "LC_ALL=C"),
	}
	for _, c := range okCases {
		if err := validate(&c); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	badCases := map[string]Connection{
		"salto de línea (\\n)": ssh("ls\nrm x"),
		"salto de línea (\\r)": ssh("ls\r"),
		"sin =":                ssh("", "SOLONOMBRE"),
		"nombre vacío":         ssh("", "=valor"),
		"empieza con número":   ssh("", "1A=x"),
		"nombre con guion":     ssh("", "MI-VAR=x"),
		"nombre con espacio":   ssh("", "MI VAR=x"),
		"una buena y una mala": ssh("", "A=1", "B C=2"),
	}
	for name, c := range badCases {
		if err := validate(&c); err == nil {
			t.Errorf("%s: no dio error", name)
		}
	}

	// Se recorta el comando y se descartan las líneas de variables vacías.
	c := ssh("  uptime  ", "", "  ", "A=1")
	if err := validate(&c); err != nil || c.InitialCommand != "uptime" || !reflect.DeepEqual(c.Env, []string{"A=1"}) {
		t.Errorf("normalizado = %+v, %v", c, err)
	}

	// En RDP se vacían, aunque vengan mal.
	r := Connection{Protocol: ProtoRDP, Host: "h", User: "u", ForwardAgent: true, InitialCommand: "a\nb", Env: []string{"mal"}}
	if err := validate(&r); err != nil || r.ForwardAgent || r.InitialCommand != "" || r.Env != nil {
		t.Errorf("rdp = %+v, %v", r, err)
	}
}

func TestAdvancedFieldsPersist(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	c, err := s.Save(ConnectionInput{Connection: Connection{
		Name: "a", Host: "h", User: "u", AuthType: AuthAgent,
		ForwardAgent: true, InitialCommand: "tmux a", Env: []string{"EDITOR=vim"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reload(t, s).Get(c.ID)
	if !ok || !got.ForwardAgent || got.InitialCommand != "tmux a" || !reflect.DeepEqual(got.Env, []string{"EDITOR=vim"}) {
		t.Fatalf("recargada = %+v", got)
	}
}

// Un connections.json de antes (sin los campos nuevos) se lee igual y, al
// volver a guardarlo, no le agrega los campos.
func TestOldConnectionsFileLoads(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	old := `{"version":1,"connections":[{"id":"c1","name":"viejo","group":"","host":"h","port":22,"user":"u","authType":"agent","hasSecret":false,"createdAt":1,"updatedAt":1}]}`
	if err := os.WriteFile(s.path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s = reload(t, s)
	c, ok := s.Get("c1")
	if !ok || c.Name != "viejo" || c.ForwardAgent || c.InitialCommand != "" || c.Env != nil {
		t.Fatalf("c1 = %+v, %v", c, ok)
	}
	if _, err := s.Save(ConnectionInput{Connection: c}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"forwardAgent", "initialCommand", "env"} {
		if strings.Contains(string(b), `"`+k+`"`) {
			t.Errorf("connections.json tiene %q sin que se haya usado:\n%s", k, b)
		}
	}
}

func TestGatewayRDPSaved(t *testing.T) {
	c := Connection{Protocol: ProtoRDP, Host: "h", User: "u", Gateway: " rds.empresa.com "}
	if err := validate(&c); err != nil || c.Gateway != "rds.empresa.com" {
		t.Fatalf("gateway = %q, %v", c.Gateway, err)
	}
}

func TestGatewayClearedForSSH(t *testing.T) {
	c := Connection{Host: "h", User: "u", Gateway: "x"}
	if err := validate(&c); err != nil || c.Gateway != "" {
		t.Fatalf("gateway = %q, %v", c.Gateway, err)
	}
}

func TestGatewayAndJumpRejected(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	j, err := s.Save(ConnectionInput{Connection: Connection{Name: "bastion", Host: "b", User: "u", AuthType: AuthAgent}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Save(ConnectionInput{Connection: Connection{
		Name: "w", Protocol: ProtoRDP, Host: "h", User: "u", Gateway: "rds", JumpID: j.ID,
	}})
	if err == nil {
		t.Fatal("se guardó una conexión con gateway y salto")
	}
	if len(s.List()) != 1 {
		t.Fatalf("hay %d conexiones", len(s.List()))
	}
}

func TestGatewayAddr(t *testing.T) {
	for in, want := range map[string]struct {
		host string
		port int
	}{
		"rds.empresa.com": {"rds.empresa.com", 443},
		"gw:8443":         {"gw", 8443},
	} {
		if h, p := GatewayAddr(in); h != want.host || p != want.port {
			t.Errorf("GatewayAddr(%q) = %q, %d", in, h, p)
		}
	}
}
