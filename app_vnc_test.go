package main

import (
	"net"
	"testing"

	"osat/internal/store"
	"osat/internal/vncproxy"
)

// Una conexión VNC exportada a un .osat vuelve igual, con su contraseña y su salto.
func TestExportImportVnc(t *testing.T) {
	src := newExportApp(t)
	mustSave(t, src, store.Connection{Name: "bastion", Host: "b", User: "root", AuthType: store.AuthAgent}, "")
	mustSave(t, src, store.Connection{Name: "escritorio", Group: "Lab", Protocol: store.ProtoVNC, Host: "localhost", Port: 5901,
		JumpID: byName(src)["bastion"].ID}, "pw-vnc")
	mustSave(t, src, store.Connection{Name: "abierto", Protocol: store.ProtoVNC, Host: "10.0.0.9"}, "")

	path := exportFile(t, src, idsOf(src, "bastion", "escritorio", "abierto"), "clave del equipo", true)

	dst := newExportApp(t)
	r, err := dst.ImportOsatScan(path, "clave del equipo")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := dst.ImportOsat(r.Items); err != nil || n != 3 {
		t.Fatalf("ImportOsat = %d, %v", n, err)
	}
	got := byName(dst)
	v := got["escritorio"]
	if v.Protocol != store.ProtoVNC || v.Group != "Lab" || v.Host != "localhost" || v.Port != 5901 || v.User != "" ||
		v.JumpID != got["bastion"].ID || dst.store.Secret(v.ID) != "pw-vnc" {
		t.Fatalf("escritorio = %+v, secreto %q", v, dst.store.Secret(v.ID))
	}
	if o := got["abierto"]; o.Protocol != store.ProtoVNC || o.Port != 5900 || o.HasSecret {
		t.Fatalf("abierto = %+v", o)
	}
}

func TestVncOpen(t *testing.T) {
	a := newExportApp(t)
	a.vnc = vncproxy.New()
	t.Cleanup(a.vnc.CloseAll)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	mustSave(t, a, store.Connection{Name: "v", Protocol: store.ProtoVNC, Host: "127.0.0.1", Port: port}, "guardada")
	mustSave(t, a, store.Connection{Name: "s", Host: "h", User: "u", AuthType: store.AuthAgent}, "")
	byN := byName(a)

	s, err := a.VncOpen(VncOpenRequest{ConnectionID: byN["v"].ID})
	if err != nil || s.ID == "" || s.URL == "" || s.Password != "guardada" {
		t.Fatalf("VncOpen = %+v, %v", s, err)
	}
	a.VncClose(s.ID)
	// La contraseña escrita en el momento gana sobre la guardada.
	s, err = a.VncOpen(VncOpenRequest{ConnectionID: byN["v"].ID, Secret: "nueva"})
	if err != nil || s.Password != "nueva" {
		t.Fatalf("VncOpen con Secret = %+v, %v", s, err)
	}
	a.VncClose(s.ID)
	if err := a.VncSaveSecret(byN["v"].ID, "recordada"); err != nil || a.store.Secret(byN["v"].ID) != "recordada" {
		t.Fatalf("VncSaveSecret: %v", err)
	}
	if _, err := a.VncOpen(VncOpenRequest{ConnectionID: byN["s"].ID}); err == nil {
		t.Error("abrió como VNC una conexión SSH")
	}
	if _, err := a.VncOpen(VncOpenRequest{ConnectionID: "no-existe"}); err == nil {
		t.Error("abrió una conexión inexistente")
	}
}
