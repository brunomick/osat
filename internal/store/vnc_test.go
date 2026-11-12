package store

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func TestVncDefaultsPort5900(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	c, err := s.Save(ConnectionInput{Connection: Connection{Protocol: ProtoVNC, Host: "srv"}, Secret: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 5900 || c.Name != "srv" || !c.HasSecret || s.Secret(c.ID) != "pw" {
		t.Fatalf("vnc = %+v, secreto %q", c, s.Secret(c.ID))
	}
	// Sin usuario ni contraseña también se guarda (VNC puede no tener autenticación).
	if _, err := s.Save(ConnectionInput{Connection: Connection{Protocol: ProtoVNC, Host: "abierto", Port: 5901}}); err != nil {
		t.Fatal(err)
	}
	for _, port := range []int{-1, 65536} {
		if _, err := s.Save(ConnectionInput{Connection: Connection{Protocol: ProtoVNC, Host: "srv", Port: port}}); err == nil {
			t.Errorf("puerto %d no dio error", port)
		}
	}
	if _, err := s.Save(ConnectionInput{Connection: Connection{Protocol: ProtoVNC}}); err == nil {
		t.Error("sin host no dio error")
	}
	got, ok := reload(t, s).Get(c.ID)
	if !ok || got.Protocol != ProtoVNC || got.Port != 5900 {
		t.Fatalf("recargada = %+v", got)
	}
}

func TestVncJumpMustBeSSH(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	ssh, err := s.Save(ConnectionInput{Connection: Connection{Name: "ssh", Host: "b", User: "u", AuthType: AuthAgent}})
	if err != nil {
		t.Fatal(err)
	}
	rdp, err := s.Save(ConnectionInput{Connection: Connection{Name: "rdp", Protocol: ProtoRDP, Host: "r", User: "u"}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.Save(ConnectionInput{Connection: Connection{Name: "vnc", Protocol: ProtoVNC, Host: "localhost", Port: 5901, JumpID: ssh.ID}})
	if err != nil || v.JumpID != ssh.ID {
		t.Fatalf("vnc por SSH = %+v, %v", v, err)
	}
	if _, err := s.Save(ConnectionInput{Connection: Connection{Name: "x", Protocol: ProtoVNC, Host: "h", JumpID: rdp.ID}}); err == nil {
		t.Error("saltar por una conexión RDP no dio error")
	}
	if _, err := s.Save(ConnectionInput{Connection: Connection{Name: "y", Protocol: ProtoVNC, Host: "h", JumpID: v.ID}}); err == nil {
		t.Error("saltar por una conexión VNC no dio error")
	}
	if _, err := s.Save(ConnectionInput{Connection: Connection{Name: "z", Host: "h", User: "u", JumpID: v.ID}}); err == nil {
		t.Error("una SSH saltando por una VNC no dio error")
	}
	if _, err := s.Save(ConnectionInput{Connection: Connection{Name: "w", Protocol: ProtoVNC, Host: "h", JumpID: "no-existe"}}); err == nil {
		t.Error("un salto inexistente no dio error")
	}
	// El bastión no se puede borrar mientras la VNC lo usa.
	if err := s.Delete(ssh.ID); err == nil {
		t.Error("se borró un bastión en uso")
	}
}

func TestVncDropsSSHFields(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	c, err := s.Save(ConnectionInput{Connection: Connection{
		Protocol: ProtoVNC, Host: "h", User: "u", Domain: "D", Gateway: "gw", AuthType: AuthKey, KeyPath: "/k",
		ForwardAgent: true, InitialCommand: "a\nb", Env: []string{"mal"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if c.User != "" || c.Domain != "" || c.Gateway != "" || c.AuthType != AuthPassword || c.KeyPath != "" ||
		c.ForwardAgent || c.InitialCommand != "" || c.Env != nil {
		t.Fatalf("vnc = %+v", c)
	}
}
