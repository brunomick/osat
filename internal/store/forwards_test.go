package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func newForwardStore(t *testing.T) *Store {
	t.Helper()
	keyring.MockInit()
	dir := t.TempDir()
	s := &Store{dir: dir, path: filepath.Join(dir, "connections.json"), conns: map[string]Connection{}}
	for _, id := range []string{"a", "b"} {
		s.conns[id] = Connection{ID: id, Name: id, Host: "h", User: "u", Port: 22}
	}
	return s
}

func TestForwardsAreSavedAndReloaded(t *testing.T) {
	s := newForwardStore(t)
	redis, err := s.SaveForward(Forward{ConnID: "a", Name: "Redis", LocalPort: 6379, Port: 6379, AutoStart: true})
	if err != nil {
		t.Fatal(err)
	}
	if redis.ID == "" || redis.Host != "localhost" {
		t.Fatalf("se esperaba id y host por defecto: %+v", redis)
	}
	if _, err := s.SaveForward(Forward{ConnID: "a", Name: "api", LocalPort: 8080, Namespace: "prod", Resource: "svc/api", Port: 80}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveForward(Forward{ConnID: "b", LocalPort: 5432, Port: 5432}); err != nil {
		t.Fatal(err)
	}

	got := s.Forwards("a")
	if len(got) != 2 || got[0].Name != "api" || got[1].Name != "Redis" {
		t.Fatalf("de a, ordenados por nombre: %+v", got)
	}
	if !got[0].IsKubernetes() || got[0].Host != "" || got[1].IsKubernetes() {
		t.Fatalf("tipos: %+v", got)
	}
	if b := s.Forwards("b"); len(b) != 1 || b[0].Name != "Puerto 5432" {
		t.Fatalf("nombre por defecto: %+v", b)
	}

	// Editar conserva el id.
	redis.LocalPort = 16379
	if _, err := s.SaveForward(redis); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.GetForward(redis.ID); f.LocalPort != 16379 || len(s.Forwards("a")) != 2 {
		t.Fatalf("edición: %+v", f)
	}

	// Un archivo nuevo los lee.
	again := &Store{dir: s.dir, path: s.path, conns: s.conns, groups: s.groups}
	if err := again.loadForwards(); err != nil {
		t.Fatal(err)
	}
	if len(again.Forwards("a")) != 2 || len(again.Forwards("b")) != 1 {
		t.Fatalf("recarga: %+v", again.Forwards(""))
	}

	if err := s.DeleteForward(redis.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.GetForward(redis.ID); ok || len(s.Forwards("a")) != 1 {
		t.Fatal("no se borró")
	}
	if err := s.DeleteForward("no-existe"); err != nil {
		t.Fatal(err)
	}
}

func TestDeletingAConnectionDeletesItsForwards(t *testing.T) {
	s := newForwardStore(t)
	s.SaveForward(Forward{ConnID: "a", LocalPort: 1000, Port: 1000})
	s.SaveForward(Forward{ConnID: "b", LocalPort: 2000, Port: 2000})
	if err := s.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if len(s.Forwards("a")) != 0 || len(s.Forwards("b")) != 1 {
		t.Fatalf("a: %+v b: %+v", s.Forwards("a"), s.Forwards("b"))
	}
	again := &Store{dir: s.dir}
	if err := again.loadForwards(); err != nil || len(again.Forwards("")) != 1 {
		t.Fatalf("lo borrado también tiene que salir del archivo: %v %+v", err, again.Forwards(""))
	}
}

// Lo que se guarda termina en un comando del servidor: nada que no sea un nombre.
func TestForwardValidation(t *testing.T) {
	ok := []Forward{
		{ConnID: "a", LocalPort: 1, Port: 65535},
		{ConnID: "a", LocalPort: 80, Port: 80, Host: "redis.internal"},
		{ConnID: "a", LocalPort: 80, Port: 80, Host: "10.0.0.5"},
		{ConnID: "a", LocalPort: 80, Port: 80, Host: "::1"},
		{ConnID: "a", LocalPort: 80, Port: 80, Namespace: "kube-system", Resource: "pod/coredns-5d78c9869d-abcde"},
		{ConnID: "a", LocalPort: 80, Port: 80, Namespace: "prod", Resource: "svc/rabbit.mq"},
		{ConnID: "a", LocalPort: 80, Port: 80, Namespace: "prod", Resource: "deployment/api"},
	}
	for _, f := range ok {
		if err := f.Validate(); err != nil {
			t.Errorf("%+v debería valer: %v", f, err)
		}
	}
	bad := []Forward{
		{LocalPort: 80, Port: 80},
		{ConnID: "a", Port: 80},
		{ConnID: "a", LocalPort: 80},
		{ConnID: "a", LocalPort: 70000, Port: 80},
		{ConnID: "a", LocalPort: 80, Port: 80, Host: "a b"},
		{ConnID: "a", LocalPort: 80, Port: 80, Host: "x;rm -rf /"},
		{ConnID: "a", LocalPort: 80, Port: 80, Host: "$(id)"},
		{ConnID: "a", LocalPort: 80, Port: 80, Namespace: "prod", Resource: "svc/a;reboot"},
		{ConnID: "a", LocalPort: 80, Port: 80, Namespace: "prod", Resource: "svc/$(id)"},
		{ConnID: "a", LocalPort: 80, Port: 80, Namespace: "prod", Resource: "secret/x"},
		{ConnID: "a", LocalPort: 80, Port: 80, Namespace: "prod", Resource: "svc/-x"},
		{ConnID: "a", LocalPort: 80, Port: 80, Namespace: "", Resource: "svc/x"},
		{ConnID: "a", LocalPort: 80, Port: 80, Namespace: "Prod", Resource: "svc/x"},
		{ConnID: "a", LocalPort: 80, Port: 80, Namespace: "p rod", Resource: "svc/x"},
		{ConnID: "a", LocalPort: 80, Port: 80, Namespace: "-n", Resource: "svc/x"},
	}
	for _, f := range bad {
		if err := f.Validate(); err == nil {
			t.Errorf("%+v no debería valer", f)
		}
	}
}

func TestForwardNeedsARealConnection(t *testing.T) {
	s := newForwardStore(t)
	if _, err := s.SaveForward(Forward{ConnID: "nope", LocalPort: 80, Port: 80}); err == nil {
		t.Fatal("sin conexión")
	}
	if _, err := s.SaveForward(Forward{ID: "x", ConnID: "a", LocalPort: 80, Port: 80}); err == nil {
		t.Fatal("editar uno que no existe")
	}
	f, _ := s.SaveForward(Forward{ConnID: "a", LocalPort: 80, Port: 80})
	f.ConnID = "b" // no se puede pasar a otra conexión pisando el id
	if _, err := s.SaveForward(f); err == nil {
		t.Fatal("cambiar de conexión")
	}
}

func TestForwardKindsValidation(t *testing.T) {
	// Remoto: Port es del servidor, Host:LocalPort el destino en esta PC.
	r := Forward{ConnID: "a", Kind: ForwardRemote, Port: 8080, LocalPort: 3000, BindAll: true}
	if err := r.Validate(); err != nil || r.Host != "localhost" || !r.BindAll || r.Name != "Puerto 8080" {
		t.Fatalf("remoto = %+v, %v", r, err)
	}
	// Dinámico: solo el puerto de esta PC; lo demás se descarta.
	d := Forward{ConnID: "a", Kind: ForwardDynamic, LocalPort: 1080, Host: "x", Port: 80, BindAll: true}
	if err := d.Validate(); err != nil || d.Host != "" || d.Port != 0 || d.BindAll || d.Name != "SOCKS 1080" {
		t.Fatalf("dinámico = %+v, %v", d, err)
	}
	// BindAll es solo del remoto.
	l := Forward{ConnID: "a", LocalPort: 80, Port: 80, BindAll: true}
	if err := l.Validate(); err != nil || l.BindAll {
		t.Fatalf("local = %+v, %v", l, err)
	}

	bad := map[string]Forward{
		"tipo desconocido":          {ConnID: "a", Kind: "otro", LocalPort: 80, Port: 80},
		"remoto sin puerto":         {ConnID: "a", Kind: ForwardRemote, LocalPort: 80},
		"remoto puerto fuera":       {ConnID: "a", Kind: ForwardRemote, LocalPort: 80, Port: 70000},
		"remoto sin destino":        {ConnID: "a", Kind: ForwardRemote, Port: 80},
		"remoto destino inválido":   {ConnID: "a", Kind: ForwardRemote, LocalPort: 80, Port: 80, Host: "a;b"},
		"remoto a Kubernetes":       {ConnID: "a", Kind: ForwardRemote, LocalPort: 80, Port: 80, Namespace: "prod", Resource: "svc/x"},
		"dinámico sin puerto local": {ConnID: "a", Kind: ForwardDynamic},
		"dinámico puerto fuera":     {ConnID: "a", Kind: ForwardDynamic, LocalPort: 70000},
		"dinámico sin conexión":     {Kind: ForwardDynamic, LocalPort: 1080},
	}
	for name, f := range bad {
		if err := f.Validate(); err == nil {
			t.Errorf("%s: %+v no debería valer", name, f)
		}
	}
}

// Un forwards.json de antes (sin kind) se lee como reenvíos locales.
func TestOldForwardsFileLoads(t *testing.T) {
	s := newForwardStore(t)
	old := `{"version":1,"forwards":[{"id":"f1","connId":"a","name":"Redis","localPort":6379,"host":"localhost","port":6379}]}`
	if err := os.WriteFile(s.forwardsPath(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.loadForwards(); err != nil {
		t.Fatal(err)
	}
	f, ok := s.GetForward("f1")
	if !ok || f.Kind != ForwardLocal || f.BindAll || f.Validate() != nil {
		t.Fatalf("f1 = %+v", f)
	}
	r, err := s.SaveForward(Forward{ConnID: "a", Kind: ForwardRemote, Port: 8080, LocalPort: 3000})
	if err != nil {
		t.Fatal(err)
	}
	again := &Store{dir: s.dir}
	if err := again.loadForwards(); err != nil {
		t.Fatal(err)
	}
	if got, _ := again.GetForward(r.ID); got.Kind != ForwardRemote || got.Port != 8080 || got.LocalPort != 3000 {
		t.Fatalf("remoto recargado = %+v", got)
	}
}
