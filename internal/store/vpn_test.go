package store

import (
	"os"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestVPNSaveLoadDelete(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	if v := s.VPN(); v.Conf != "" || v.Enabled {
		t.Fatalf("sin VPN guardada: %+v", v)
	}
	if err := s.SaveVPN(VPN{Conf: "[Interface]\nAddress = 10.0.0.2/32", Enabled: true}, "CLAVE="); err != nil {
		t.Fatal(err)
	}
	if v := s.VPN(); !v.Enabled || !strings.Contains(v.Conf, "10.0.0.2") {
		t.Errorf("VPN = %+v", v)
	}
	if s.VPNKey() != "CLAVE=" {
		t.Errorf("VPNKey = %q", s.VPNKey())
	}
	// La clave no queda en el archivo.
	b, _ := os.ReadFile(s.vpnPath())
	if strings.Contains(string(b), "CLAVE=") {
		t.Errorf("la clave quedó en vpn.json: %s", b)
	}
	// Guardar sin clave conserva la del keyring.
	if err := s.SaveVPN(VPN{Conf: "x", Enabled: false}, ""); err != nil {
		t.Fatal(err)
	}
	if s.VPNKey() != "CLAVE=" {
		t.Errorf("VPNKey después de guardar sin clave = %q", s.VPNKey())
	}
	if err := s.DeleteVPN(); err != nil {
		t.Fatal(err)
	}
	if v := s.VPN(); v.Conf != "" || s.VPNKey() != "" {
		t.Errorf("después de borrar: %+v, clave %q", v, s.VPNKey())
	}
}
