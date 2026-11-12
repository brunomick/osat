package remote

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"osat/internal/portfwd"
	"osat/internal/store"
)

// echoBackend contesta en mayúsculas lo que recibe hasta que el cliente cierra su envío.
func echoBackend(t *testing.T) (port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				b, _ := io.ReadAll(c)
				c.Write([]byte(strings.ToUpper(string(b))))
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func through(t *testing.T, port int, msg string) string {
	t.Helper()
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write([]byte(msg))
	c.(*net.TCPConn).CloseWrite()
	b, _ := io.ReadAll(c)
	return string(b)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("no se cumplió a tiempo: %s", what)
}

// forwardRig: un servidor SSH de prueba como sesión "srv" de la conexión "c".
func forwardRig(t *testing.T, run func(cmd string, ch ssh.Channel) int) (*Manager, *sharedClient) {
	t.Helper()
	m := newBridgeManager(t)
	t.Cleanup(m.CloseAll)
	sh := newSharedClient(execServer(t, run))
	m.register(&Session{ID: "srv", Kind: KindTunnel, ConnID: "c", shared: sh})
	return m, sh
}

func stateOf(m *Manager, id string) (ForwardState, bool) {
	for _, s := range m.ForwardStates("c") {
		if s.ID == id {
			return s, true
		}
	}
	return ForwardState{}, false
}

func TestForwardAServerPort(t *testing.T) {
	backend := echoBackend(t)
	m, sh := forwardRig(t, nil)
	local := portfwd.FreePort(20000)

	rule := store.Forward{ID: "redis", ConnID: "c", Name: "Redis", LocalPort: local, Host: "127.0.0.1", Port: backend}
	if err := m.StartForward("tab", "srv", rule); err != nil {
		t.Fatal(err)
	}
	if sh.refs != 2 {
		t.Fatalf("el reenvío toma una referencia de la conexión: refs=%d", sh.refs)
	}
	if got := through(t, local, "ping"); got != "PING" {
		t.Fatalf("respuesta: %q", got)
	}
	waitFor(t, "estadísticas", func() bool { s, _ := stateOf(m, "redis"); return s.Total == 1 && s.Active == 0 })
	s, _ := stateOf(m, "redis")
	if s.Status != FwRunning || s.LocalPort != local || s.BytesOut != 4 || s.BytesIn != 4 || s.Warning != "" || s.Error != "" {
		t.Fatalf("estado: %+v", s)
	}
	if err := m.StartForward("tab", "srv", rule); err == nil {
		t.Fatal("arrancar uno ya en marcha tiene que fallar")
	}

	m.StopForward("redis")
	if sh.refs != 1 {
		t.Fatalf("al detenerlo se suelta la conexión: refs=%d", sh.refs)
	}
	if _, ok := stateOf(m, "redis"); ok {
		t.Fatal("un reenvío detenido no figura")
	}
	if _, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(local)), time.Second); err == nil {
		t.Fatal("el puerto debería estar libre")
	}
	m.StopForward("redis") // idempotente
}

func TestForwardToAClosedPortShowsAWarning(t *testing.T) {
	m, _ := forwardRig(t, nil)
	local := portfwd.FreePort(20100)
	if err := m.StartForward("tab", "srv", store.Forward{ID: "x", ConnID: "c", Name: "Nada", LocalPort: local, Host: "127.0.0.1", Port: 1}); err != nil {
		t.Fatal(err)
	}
	through(t, local, "hola")
	waitFor(t, "aviso", func() bool { s, _ := stateOf(m, "x"); return s.Warning != "" })
	s, _ := stateOf(m, "x")
	if s.Status != FwRunning || !strings.Contains(s.Warning, "no hay nada escuchando en 127.0.0.1:1") {
		t.Fatalf("estado: %+v", s)
	}
}

func TestForwardLocalPortProblems(t *testing.T) {
	backend := echoBackend(t)
	m, sh := forwardRig(t, nil)
	local := portfwd.FreePort(20200)

	// Otro programa tiene el puerto.
	taken, err := portfwd.Listen(local, nil)
	if err != nil {
		t.Fatal(err)
	}
	rule := store.Forward{ID: "a", ConnID: "c", Name: "A", LocalPort: local, Host: "127.0.0.1", Port: backend}
	err = m.StartForward("tab", "srv", rule)
	if err == nil || !strings.Contains(err.Error(), "otro programa") {
		t.Fatalf("error: %v", err)
	}
	if s, ok := stateOf(m, "a"); !ok || s.Status != FwError || s.Error == "" {
		t.Fatalf("el motivo tiene que verse en el reenvío: %+v", s)
	}
	if sh.refs != 1 {
		t.Fatalf("un reenvío que no arrancó no retiene la conexión: refs=%d", sh.refs)
	}

	// Liberado el puerto, se reintenta con el mismo reenvío.
	taken.Close()
	if err := m.StartForward("tab", "srv", rule); err != nil {
		t.Fatal(err)
	}

	// Otro reenvío nuestro con el mismo puerto de esta PC: se dice cuál.
	other := store.Forward{ID: "b", ConnID: "c", Name: "B", LocalPort: local, Host: "127.0.0.1", Port: backend}
	if err := m.StartForward("tab", "srv", other); err == nil || !strings.Contains(err.Error(), "“A”") {
		t.Fatalf("error: %v", err)
	}
}

func TestForwardsStopWithTheirTabAndWithTheApp(t *testing.T) {
	backend := echoBackend(t)
	m, sh := forwardRig(t, nil)
	a, b, c := portfwd.FreePort(20300), portfwd.FreePort(20400), portfwd.FreePort(20500)
	for i, o := range []struct {
		id, owner string
		port      int
	}{{"1", "tab-1", a}, {"2", "tab-1", b}, {"3", "tab-2", c}} {
		if err := m.StartForward(o.owner, "srv", store.Forward{ID: o.id, ConnID: "c", LocalPort: o.port, Host: "127.0.0.1", Port: backend}); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	m.StopForwardsOf("tab-1")
	if len(m.ForwardStates("c")) != 1 || sh.refs != 2 {
		t.Fatalf("quedaba solo el de tab-2: %+v refs=%d", m.ForwardStates("c"), sh.refs)
	}
	if got := through(t, c, "x"); got != "X" {
		t.Fatalf("el de tab-2 tenía que seguir andando: %q", got)
	}
	m.CloseAll()
	if len(m.ForwardStates("c")) != 0 {
		t.Fatal("al cerrar la app no queda ninguno")
	}
}

func TestForwardEndsWhenTheServerConnectionDrops(t *testing.T) {
	backend := echoBackend(t)
	m, sh := forwardRig(t, nil)
	local := portfwd.FreePort(20600)
	if err := m.StartForward("tab", "srv", store.Forward{ID: "x", ConnID: "c", LocalPort: local, Host: "127.0.0.1", Port: backend}); err != nil {
		t.Fatal(err)
	}
	sh.client.Close() // el servidor se cae / se corta la red
	waitFor(t, "estado de error", func() bool { s, _ := stateOf(m, "x"); return s.Status == FwError })
	if s, _ := stateOf(m, "x"); !strings.Contains(s.Error, "se cortó la conexión") {
		t.Fatalf("estado: %+v", s)
	}
	// Con la conexión caída no se puede arrancar otro: la pestaña tiene que reconectar.
	if err := m.StartForward("tab", "srv", store.Forward{ID: "y", ConnID: "c", LocalPort: portfwd.FreePort(20650), Host: "127.0.0.1", Port: backend}); err == nil || !strings.Contains(err.Error(), "se cortó la conexión") {
		t.Fatalf("error: %v", err)
	}
	var ke *Error
	if err := m.StartForward("tab", "srv", store.Forward{ID: "z", ConnID: "c", LocalPort: portfwd.FreePort(20660), Host: "127.0.0.1", Port: backend}); !errors.As(err, &ke) || ke.Code != CodeServerGone {
		t.Fatalf("el error no trae el código SERVER_GONE: %v", err)
	}
	waitFor(t, "puerto libre", func() bool {
		c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(local)), 200*time.Millisecond)
		if err == nil {
			c.Close()
		}
		return err != nil
	})
}

func TestForwardNeedsARemoteSession(t *testing.T) {
	m := newBridgeManager(t)
	ok := store.Forward{ID: "x", ConnID: "c", LocalPort: 20700, Host: "localhost", Port: 80}
	if err := m.StartForward("tab", "no-existe", ok); err == nil {
		t.Fatal("sesión inexistente")
	}
	if err := m.StartForward("tab", openCmd(t, m), ok); err == nil {
		t.Fatal("una terminal de este equipo no tiene servidor")
	}
	if err := m.StartForward("tab", "srv", store.Forward{ID: "y", ConnID: "c", LocalPort: 20700, Host: "a;b", Port: 80}); err == nil {
		t.Fatal("un reenvío inválido no debe llegar a ningún comando")
	}
}

// ---------- Kubernetes ----------

const nsJSON = `{"items":[{"kind":"Namespace","metadata":{"name":"prod"}},{"kind":"Namespace","metadata":{"name":"default"}}]}`

const podsJSON = `{"items":[
 {"kind":"Pod","metadata":{"name":"api-7d9"},"spec":{"containers":[{"ports":[{"containerPort":8080,"protocol":"TCP","name":"http"},{"containerPort":53,"protocol":"UDP"}]}]},"status":{"phase":"Running"}},
 {"kind":"Pod","metadata":{"name":"worker-1"},"spec":{"containers":[{}]},"status":{"phase":"Running","containerStatuses":[{"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}},
 {"kind":"Service","metadata":{"name":"redis"},"spec":{"type":"ClusterIP","ports":[{"port":6379,"protocol":"TCP","name":"redis"}]}},
 {"kind":"Service","metadata":{"name":"rabbitmq"},"spec":{"type":"ClusterIP","ports":[{"port":5672,"name":"amqp"},{"port":15672,"name":"management"}]}}
]}`

func TestParseKubernetesLists(t *testing.T) {
	names, err := parseNamespaces(nsJSON)
	if err != nil || strings.Join(names, ",") != "default,prod" {
		t.Fatalf("namespaces: %v %v", names, err)
	}
	items, err := parseResources(podsJSON)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		var ports []string
		for _, p := range it.Ports {
			ports = append(ports, strconv.Itoa(p.Port))
		}
		got = append(got, fmt.Sprintf("%s/%s[%s]{%s}", it.Kind, it.Name, it.Status, strings.Join(ports, ",")))
	}
	want := "svc/rabbitmq[ClusterIP]{5672,15672} svc/redis[ClusterIP]{6379} pod/api-7d9[Running]{8080} pod/worker-1[CrashLoopBackOff]{}"
	if strings.Join(got, " ") != want {
		t.Fatalf("recursos:\n got %s\nwant %s", strings.Join(got, " "), want)
	}
	if _, err := parseResources("no es json"); err == nil {
		t.Fatal("una respuesta que no es JSON tiene que dar error")
	}
}

// fakeKubectl hace de "kubectl" del servidor: contesta las listas y, en port-forward, abre un puerto
// que lleva a backend. stopped cuenta los port-forward que terminaron (los cortó el cliente).
func fakeKubectl(t *testing.T, backend int, stopped *atomic.Int32) func(string, ssh.Channel) int {
	return func(cmd string, ch ssh.Channel) int {
		switch {
		case strings.HasPrefix(cmd, "kubectl config view"):
			io.WriteString(ch, "prod\n")
		case cmd == "kubectl get namespaces -o json":
			io.WriteString(ch, nsJSON)
		case cmd == "kubectl get pods,services -n prod -o json":
			io.WriteString(ch, podsJSON)
		case strings.HasPrefix(cmd, "kubectl get"):
			io.WriteString(ch.Stderr(), `Error from server (Forbidden): namespaces is forbidden: User "dev" cannot list resource`)
			return 1
		case cmd == "kubectl port-forward -n prod svc/redis :6379 --address 127.0.0.1":
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return 1
			}
			defer ln.Close()
			go func() {
				for {
					c, err := ln.Accept()
					if err != nil {
						return
					}
					go func() {
						b, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(backend)))
						if err != nil {
							c.Close()
							return
						}
						go func() { io.Copy(b, c); b.(*net.TCPConn).CloseWrite() }()
						io.Copy(c, b)
						c.Close()
					}()
				}
			}()
			p := ln.Addr().(*net.TCPAddr).Port
			fmt.Fprintf(ch, "Forwarding from 127.0.0.1:%d -> 6379\r\n", p)
			buf := make([]byte, 1)
			for { // hasta que el cliente cierra la sesión (o manda Ctrl+C)
				if _, err := ch.Read(buf); err != nil || buf[0] == 3 {
					break
				}
			}
			stopped.Add(1)
			return 130
		case strings.HasPrefix(cmd, "kubectl port-forward"):
			io.WriteString(ch, "error: pods \"gone\" not found\r\n")
			return 1
		default:
			io.WriteString(ch, "bash: kubectl: command not found\r\n")
			return 127
		}
		return 0
	}
}

func TestListKubernetes(t *testing.T) {
	var stopped atomic.Int32
	m, sh := forwardRig(t, fakeKubectl(t, 1, &stopped))

	ns, err := m.K8sNamespaces("srv")
	if err != nil || ns.Current != "prod" || strings.Join(ns.Names, ",") != "default,prod" {
		t.Fatalf("namespaces: %+v %v", ns, err)
	}
	items, err := m.K8sResources("srv", "prod")
	if err != nil || len(items) != 4 || items[0].Name != "rabbitmq" {
		t.Fatalf("recursos: %+v %v", items, err)
	}
	// Permiso denegado: se ve el mensaje de kubectl.
	if _, err := m.K8sResources("srv", "otro"); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("error: %v", err)
	}
	// El namespace termina en un comando: solo nombres válidos.
	for _, bad := range []string{"", "prod; reboot", "$(id)", "A b", "-A"} {
		if _, err := m.K8sResources("srv", bad); err == nil {
			t.Fatalf("namespace %q no debería valer", bad)
		}
	}
	if sh.refs != 1 {
		t.Fatalf("las consultas no retienen la conexión: refs=%d", sh.refs)
	}
	if _, err := m.K8sNamespaces("no-existe"); err == nil {
		t.Fatal("sesión inexistente")
	}
}

func TestForwardAKubernetesService(t *testing.T) {
	backend := echoBackend(t)
	var stopped atomic.Int32
	m, sh := forwardRig(t, fakeKubectl(t, backend, &stopped))
	local := portfwd.FreePort(21000)

	rule := store.Forward{ID: "redis", ConnID: "c", Name: "Redis", LocalPort: local, Namespace: "prod", Resource: "svc/redis", Port: 6379}
	if err := m.StartForward("tab", "srv", rule); err != nil {
		t.Fatal(err)
	}
	if got := through(t, local, "hola k8s"); got != "HOLA K8S" {
		t.Fatalf("respuesta: %q", got)
	}
	if s, _ := stateOf(m, "redis"); s.Status != FwRunning {
		t.Fatalf("estado: %+v", s)
	}

	m.StopForward("redis")
	waitFor(t, "kubectl cortado en el servidor", func() bool { return stopped.Load() == 1 })
	if sh.refs != 1 {
		t.Fatalf("refs=%d", sh.refs)
	}
}

func TestForwardAKubernetesFailureExplainsWhy(t *testing.T) {
	var stopped atomic.Int32
	m, sh := forwardRig(t, fakeKubectl(t, 1, &stopped))

	gone := store.Forward{ID: "gone", ConnID: "c", LocalPort: portfwd.FreePort(21100), Namespace: "prod", Resource: "pod/gone", Port: 80}
	err := m.StartForward("tab", "srv", gone)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error: %v", err)
	}
	if s, _ := stateOf(m, "gone"); s.Status != FwError || !strings.Contains(s.Error, "not found") {
		t.Fatalf("estado: %+v", s)
	}
	if sh.refs != 1 {
		t.Fatalf("refs=%d", sh.refs)
	}
	if _, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(gone.LocalPort)), 300*time.Millisecond); err == nil {
		t.Fatal("el puerto local debería haberse liberado")
	}

	// Sin kubectl en el servidor.
	m2, _ := forwardRig(t, func(cmd string, ch ssh.Channel) int {
		io.WriteString(ch, "bash: kubectl: command not found\r\n")
		return 127
	})
	err = m2.StartForward("tab", "srv", store.Forward{ID: "n", ConnID: "c", LocalPort: portfwd.FreePort(21200), Namespace: "prod", Resource: "svc/x", Port: 80})
	if err == nil || !strings.Contains(err.Error(), "kubectl no está instalado") {
		t.Fatalf("error: %v", err)
	}
	if _, err := m2.K8sNamespaces("srv"); err == nil || !strings.Contains(err.Error(), "kubectl no está instalado") {
		t.Fatalf("error: %v", err)
	}
}
