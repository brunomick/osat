package store

import (
	"testing"

	"github.com/zalando/go-keyring"
)

// Una conexión MongoDB guarda la cadena sin la contraseña (va al llavero) y
// de la cadena salen el host, el puerto y el usuario.
func TestSaveMongo(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	c, err := s.Save(ConnectionInput{Connection: Connection{
		Protocol: ProtoMongo, URI: "mongodb://lector:p%40ss@db1.example.com:27018,db2.example.com/ventas?authSource=admin",
		// Lo que no es de MongoDB se descarta.
		Domain: "X", KeyPath: "k", AuthType: AuthKey, UseDefaultCreds: true, InitialCommand: "ls", Env: []string{"A=1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if c.URI != "mongodb://lector@db1.example.com:27018,db2.example.com/ventas?authSource=admin" {
		t.Fatalf("URI = %q", c.URI)
	}
	if c.Host != "db1.example.com" || c.Port != 27018 || c.User != "lector" || c.Name != "lector@db1.example.com" {
		t.Fatalf("conexión = %+v", c)
	}
	if c.Domain != "" || c.KeyPath != "" || c.AuthType != AuthPassword || c.UseDefaultCreds || c.InitialCommand != "" || c.Env != nil {
		t.Fatalf("quedaron campos de otros protocolos: %+v", c)
	}
	if !c.HasSecret || s.Secret(c.ID) != "p@ss" {
		t.Fatalf("la contraseña de la cadena no fue al llavero: %+v %q", c, s.Secret(c.ID))
	}
	// La contraseña del formulario gana sobre la de la cadena.
	c.URI = "mongodb://lector:vieja@db1.example.com:27018/ventas"
	c, err = s.Save(ConnectionInput{Connection: c, Secret: "nueva"})
	if err != nil || s.Secret(c.ID) != "nueva" || c.URI != "mongodb://lector@db1.example.com:27018/ventas" {
		t.Fatalf("%+v %v %q", c, err, s.Secret(c.ID))
	}
}

// Sin usuario vale (MongoDB sin autenticación) y no se usa el por defecto.
func TestSaveMongoWithoutUserIgnoresDefaults(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	if _, err := s.SaveDefaults(DefaultCredsInput{DefaultCreds: DefaultCreds{User: "root"}, Secret: "por-defecto"}); err != nil {
		t.Fatal(err)
	}
	c, err := s.Save(ConnectionInput{Connection: Connection{Protocol: ProtoMongo, URI: "mongodb://localhost"}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "localhost" || c.Port != 27017 || c.User != "" || c.Name != "localhost" {
		t.Fatalf("conexión = %+v", c)
	}
	if got := s.WithDefaults(c); got.User != "" {
		t.Fatalf("WithDefaults le puso el usuario por defecto: %+v", got)
	}
	if got := s.SecretFor(c); got != "" {
		t.Fatalf("SecretFor usó la contraseña por defecto: %q", got)
	}
	if n, err := s.SetUseDefaultCreds("", true); err != nil || n != 0 {
		t.Fatalf("SetUseDefaultCreds marcó la conexión MongoDB: %d %v", n, err)
	}
}

func TestSaveMongoRejectsBadURI(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	for _, uri := range []string{"", "localhost:27017", "mongodb://"} {
		if _, err := s.Save(ConnectionInput{Connection: Connection{Protocol: ProtoMongo, URI: uri}}); err == nil {
			t.Errorf("%q debería fallar", uri)
		}
	}
}

// Solo se salta a través de conexiones SSH: una MongoDB no sirve de salto,
// pero puede usar uno (el túnel SSH).
func TestMongoJumps(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	ssh, err := s.Save(ConnectionInput{Connection: Connection{Name: "bastion", Host: "b", User: "u", AuthType: AuthAgent}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Save(ConnectionInput{Connection: Connection{Protocol: ProtoMongo, URI: "mongodb://10.0.0.5", JumpID: ssh.ID}})
	if err != nil || m.JumpID != ssh.ID {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := s.Save(ConnectionInput{Connection: Connection{Name: "x", Host: "x", User: "u", AuthType: AuthAgent, JumpID: m.ID}}); err == nil {
		t.Fatal("una conexión MongoDB no debería servir de salto")
	}
	// Una SSH no guarda una cadena de MongoDB.
	c, err := s.Save(ConnectionInput{Connection: Connection{Name: "y", Host: "y", User: "u", AuthType: AuthAgent, URI: "mongodb://h"}})
	if err != nil || c.URI != "" {
		t.Fatalf("%+v %v", c, err)
	}
}
