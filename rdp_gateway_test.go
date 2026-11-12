package main

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"testing"

	"osat/internal/rdgateway"
	"osat/internal/rdpproxy"
	"osat/internal/remote"
	"osat/internal/store"
)

// Con un gateway cargado, RdpOpen va por el gateway (y no directo al equipo).
func TestRdpOpenUsesGateway(t *testing.T) {
	a := newExportApp(t)
	rp, err := rdpproxy.New(filepath.Join(t.TempDir(), "rdp_known_certs"))
	if err != nil {
		t.Fatal(err)
	}
	a.rdp = rp
	// Un "equipo" que sí escucha: si RdpOpen fuera directo, llegaría acá.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	reached := make(chan struct{}, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			reached <- struct{}{}
			c.Close()
		}
	}()
	mustSave(t, a, store.Connection{Name: "ts", Protocol: store.ProtoRDP, Host: "127.0.0.1",
		Port: ln.Addr().(*net.TCPAddr).Port, User: "u", Domain: "D", Gateway: "127.0.0.1:1"}, "pw")
	_, err = a.RdpOpen(RdpOpenRequest{ConnectionID: byName(a)["ts"].ID})
	var re *remote.Error
	if !errors.As(err, &re) || re.Code != remote.CodeGateway || re.Data["kind"] != "unreachable" ||
		re.Data["step"] != "tcp" || re.Data["gateway"] != "127.0.0.1:"+strconv.Itoa(1) {
		t.Fatalf("err = %v", err)
	}
	select {
	case <-reached:
		t.Fatal("RdpOpen fue directo al equipo")
	default:
	}
}

// Usuario o contraseña rechazados por el gateway: AUTH_FAILED, para que el
// frontend vuelva a pedir la contraseña. Los demás: GATEWAY, con el paso y el código.
func TestGatewayErrorMapping(t *testing.T) {
	err := gatewayError(&rdgateway.Error{Kind: rdgateway.KindAuth, Step: rdgateway.StepAuth, Gateway: "gw:443"})
	var re *remote.Error
	if !errors.As(err, &re) || re.Code != remote.CodeAuthFailed || re.Data["kind"] != "gateway" || re.Data["gateway"] != "gw:443" {
		t.Fatalf("auth: %v", err)
	}
	other := &rdgateway.Error{Kind: rdgateway.KindNotAuthorized, Step: rdgateway.StepTunnelAuth, Reason: "cap", Gateway: "gw:443", Code: 0x800759DB}
	if err := gatewayError(other); !errors.As(err, &re) || re.Code != remote.CodeGateway || re.Data["kind"] != "notAuthorized" ||
		re.Data["reason"] != "cap" || re.Data["step"] != "tunnelAuth" || re.Data["code"] != "0x800759DB" {
		t.Fatalf("otro: %v", err)
	}
	cert := remote.NewError(remote.CodeRdpCertUnknown, "x", map[string]string{})
	if err := gatewayError(fmt.Errorf("envuelto: %w", cert)); err != cert {
		t.Fatalf("certificado: %v", err)
	}
}

// rdpError: el canal al equipo que el gateway rechaza llega como GATEWAY (no
// como un error "del bastión"), la espera vencida también, y los pasos con el
// servidor RDP después del TCP como RDP_SERVER con el paso.
func TestRdpErrorMapping(t *testing.T) {
	c := store.Connection{Host: "srv", Port: 3389, Gateway: "gw.corp"}
	code := func(err error) (string, map[string]string) {
		var re *remote.Error
		if !errors.As(err, &re) {
			t.Fatalf("sin código: %v", err)
		}
		return re.Code, re.Data
	}
	denied := &rdgateway.Error{Kind: rdgateway.KindResourceDenied, Step: rdgateway.StepChannel, Gateway: "gw.corp:443", Target: "srv:3389", Code: 0x800759DA}
	if k, d := code(rdpError(fmt.Errorf("envuelto: %w", denied), c)); k != remote.CodeGateway || d["kind"] != "resourceDenied" || d["target"] != "srv:3389" {
		t.Fatalf("RAP: %s %v", k, d)
	}
	if k, d := code(rdpError(fmt.Errorf("x: %w", rdpproxy.ErrViaTimeout), c)); k != remote.CodeGateway ||
		d["kind"] != "targetUnreachable" || d["reason"] != "timeout" || d["step"] != "channel" || d["gateway"] != "gw.corp:443" {
		t.Fatalf("espera: %s %v", k, d)
	}
	se := &rdpproxy.ServerError{Step: "tls", Err: errors.New("handshake")}
	if k, d := code(rdpError(se, c)); k != remote.CodeRdpServer || d["kind"] != "tls" || d["host"] != "srv:3389" || d["gateway"] != "gw.corp" {
		t.Fatalf("TLS con el equipo: %s %v", k, d)
	}
	plain := errors.New("otro")
	if err := rdpError(plain, c); err != plain {
		t.Fatalf("otro: %v", err)
	}
}
