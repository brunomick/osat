package store

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func TestJumpValidation(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	save := func(c Connection) (Connection, error) {
		return s.Save(ConnectionInput{Connection: c})
	}
	ssh := func(name, jump string) Connection {
		return Connection{Name: name, Host: name, User: "u", AuthType: AuthAgent, JumpID: jump}
	}
	a, err := save(ssh("a", ""))
	if err != nil {
		t.Fatal(err)
	}
	b, err := save(ssh("b", a.ID))
	if err != nil || b.JumpID != a.ID {
		t.Fatalf("b = %+v, %v", b, err)
	}
	if _, err := save(ssh("x", "no-existe")); err == nil {
		t.Error("un salto inexistente no dio error")
	}
	// Ciclos: a saltando por sí misma, o a → b → a.
	a.JumpID = a.ID
	if _, err := save(a); err == nil {
		t.Error("una conexión que salta por sí misma no dio error")
	}
	a.JumpID = b.ID
	if _, err := save(a); err == nil {
		t.Error("un ciclo a → b → a no dio error")
	}
	// Una RDP también puede saltar, pero solo se salta a través de conexiones SSH.
	r, err := s.Save(ConnectionInput{Connection: Connection{Name: "r", Protocol: ProtoRDP, Host: "r", User: "u", JumpID: a.ID}})
	if err != nil || r.JumpID != a.ID {
		t.Fatalf("rdp = %+v, %v", r, err)
	}
	if _, err := save(ssh("y", r.ID)); err == nil {
		t.Error("saltar a través de una conexión RDP no dio error")
	}
	if err := s.Delete(r.ID); err != nil {
		t.Fatal(err)
	}
	// No se puede borrar un bastión que otra conexión usa.
	if err := s.Delete(a.ID); err == nil {
		t.Error("se borró un bastión en uso")
	}
	if err := s.Delete(b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(a.ID); err != nil {
		t.Fatalf("bastión ya sin uso: %v", err)
	}
	if got := reload(t, s); len(got.conns) != 0 {
		t.Fatalf("quedaron %d conexiones", len(got.conns))
	}
}

func TestDeleteGroupWithUsedJump(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	b, err := s.Save(ConnectionInput{Connection: Connection{Name: "bastion", Group: "Infra", Host: "b", User: "u", AuthType: AuthAgent}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ConnectionInput{Connection: Connection{Name: "db", Group: "Clientes", Host: "d", User: "u", AuthType: AuthAgent, JumpID: b.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteWithin("Infra"); err == nil {
		t.Fatal("se borró un grupo con un bastión que usa otra conexión")
	}
	// Todo junto (bastión y quien lo usa) sí se puede borrar.
	if n, err := s.DeleteWithin(""); err != nil || n != 2 {
		t.Fatalf("borrar todo = %d, %v", n, err)
	}
}
