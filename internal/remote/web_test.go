package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"osat/internal/web"
)

// Una carpeta del servidor (acá SFTP en memoria) se ve por HTTP, sin salirse de su raíz.
func TestServeRemoteFolderOverSFTP(t *testing.T) {
	cl := memSFTP(t)
	for _, d := range []string{"/srv", "/srv/site", "/srv/site/css"} {
		if err := cl.Mkdir(d); err != nil {
			t.Fatal(err)
		}
	}
	put(t, cl, "/srv/site/index.html", "<link rel=stylesheet href=css/a.css>")
	put(t, cl, "/srv/site/css/a.css", "body{color:red}")
	put(t, cl, "/srv/secret.txt", "fuera de la raíz")

	hub := web.NewHub()
	defer hub.CloseAll()
	site, err := hub.Add("tab", web.Files(sftpSource{cl: cl, root: "/srv/site"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	get := func(p string) (*http.Response, string) {
		t.Helper()
		resp, err := http.Get(site.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}

	if resp, body := get("/"); resp.StatusCode != 200 || !strings.Contains(body, "css/a.css") {
		t.Fatalf("index: %d %q", resp.StatusCode, body)
	}
	if resp, body := get("/css/a.css"); resp.StatusCode != 200 || body != "body{color:red}" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("css: %d %q %q", resp.StatusCode, body, resp.Header.Get("Content-Type"))
	}
	if resp, body := get("/css/"); resp.StatusCode != 200 || !strings.Contains(body, `href="a.css"`) {
		t.Fatalf("listado: %d %q", resp.StatusCode, body)
	}
	for _, p := range []string{"/../secret.txt", "/%2e%2e/secret.txt", "/css/../../secret.txt"} {
		if resp, body := get(p); resp.StatusCode == 200 || strings.Contains(body, "fuera de la raíz") {
			t.Fatalf("%s salió de la raíz: %d %q", p, resp.StatusCode, body)
		}
	}
}

// forwardingServer es un servidor SSH mínimo (sin autenticación) que solo sabe
// reenviar puertos (direct-tcpip), como un sshd con AllowTcpForwarding.
func forwardingServer(t *testing.T) *ssh.Client { return execServer(t, nil) }

// execServer además corre comandos (exec) con run, que imprime en el canal y devuelve el código de salida.
func execServer(t *testing.T, run func(cmd string, ch ssh.Channel) int) *ssh.Client {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					if nch.ChannelType() == "session" && run != nil {
						ch, chReqs, err := nch.Accept()
						if err != nil {
							continue
						}
						go serveExec(ch, chReqs, run)
						continue
					}
					if nch.ChannelType() != "direct-tcpip" {
						nch.Reject(ssh.UnknownChannelType, "solo direct-tcpip")
						continue
					}
					var p struct {
						Host     string
						Port     uint32
						OrigHost string
						OrigPort uint32
					}
					if err := ssh.Unmarshal(nch.ExtraData(), &p); err != nil {
						nch.Reject(ssh.ConnectionFailed, err.Error())
						continue
					}
					go func() {
						dst, err := net.Dial("tcp", net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port))))
						if err != nil {
							nch.Reject(ssh.ConnectionFailed, "Connection refused") // como dice OpenSSH
							return
						}
						ch, chReqs, err := nch.Accept()
						if err != nil {
							dst.Close()
							return
						}
						go ssh.DiscardRequests(chReqs)
						go func() { io.Copy(dst, ch); dst.(*net.TCPConn).CloseWrite() }() // como sshd: solo cierra el envío
						go func() { io.Copy(ch, dst); ch.Close() }()
					}()
				}
			}()
		}
	}()

	client, err := ssh.Dial("tcp", ln.Addr().String(), &ssh.ClientConfig{User: "u", HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func serveExec(ch ssh.Channel, reqs <-chan *ssh.Request, run func(cmd string, ch ssh.Channel) int) {
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			req.Reply(true, nil)
		case "exec":
			var p struct{ Cmd string }
			ssh.Unmarshal(req.Payload, &p)
			req.Reply(true, nil)
			go func() {
				code := run(p.Cmd, ch)
				ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{uint32(code)}))
				ch.Close()
			}()
		default:
			req.Reply(false, nil)
		}
	}
}

// Un puerto "del servidor" se ve por la conexión SSH y, al cerrar la pestaña, se suelta la conexión.
func TestTunnelShowsAServerPortThroughSSH(t *testing.T) {
	var sawHost string
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHost = r.Host
		w.Header().Set("X-Frame-Options", "DENY")
		io.WriteString(w, "app del servidor")
	}))
	defer app.Close()
	appPort, _ := strconv.Atoi(app.URL[strings.LastIndex(app.URL, ":")+1:])

	m := newBridgeManager(t)
	sh := newSharedClient(forwardingServer(t))
	m.register(&Session{ID: "srv", Kind: KindSFTP, ConnID: "c", shared: sh})

	site, err := m.Tunnel("tab-1", "srv", web.Target{Host: "127.0.0.1", Port: appPort})
	if err != nil {
		t.Fatal(err)
	}
	if sh.refs != 2 {
		t.Fatalf("el túnel debe tomar una referencia de la conexión: refs=%d", sh.refs)
	}
	resp, err := http.Get(site.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "app del servidor" || resp.Header.Get("X-Frame-Options") != "" {
		t.Fatalf("respuesta: %q %v", body, resp.Header)
	}
	if want := "127.0.0.1:" + strconv.Itoa(appPort); sawHost != want {
		t.Fatalf("el servidor debe ver su propio nombre: %q (esperado %q)", sawHost, want)
	}

	// Un puerto donde no hay nada: página de error, no un cuelgue.
	dead, err := m.Tunnel("tab-1", "srv", web.Target{Host: "127.0.0.1", Port: 1})
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.Get(dead.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("puerto sin servicio: %d", resp.StatusCode)
	}

	m.CloseWeb("tab-1")
	if sh.refs != 1 {
		t.Fatalf("al cerrar la pestaña se suelta la conexión: refs=%d", sh.refs)
	}
	if _, err := http.Get(site.URL + "/"); err == nil {
		t.Fatal("el sitio debería estar cerrado")
	}
}

// Los servicios de esta PC se muestran en el iframe aunque prohíban que los enmarquen; lo que no es loopback, no.
func TestLocalProxyOnlyForLoopback(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		io.WriteString(w, "app local")
	}))
	defer app.Close()
	port, _ := strconv.Atoi(app.URL[strings.LastIndex(app.URL, ":")+1:])

	m := newBridgeManager(t)
	defer m.CloseAll()
	site, err := m.LocalProxy("tab", web.Target{Host: "127.0.0.1", Port: port})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(site.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "app local" || resp.Header.Get("X-Frame-Options") != "" {
		t.Fatalf("%q %v", body, resp.Header)
	}
	for _, host := range []string{"example.com", "192.168.1.10", "10.0.0.1"} {
		if _, err := m.LocalProxy("tab", web.Target{Host: host, Port: 80}); err == nil {
			t.Fatalf("%s no es de esta PC y no debería pasar por el proxy local", host)
		}
	}
	m.CloseWeb("tab")
	if _, err := http.Get(site.URL + "/"); err == nil {
		t.Fatal("el sitio debería estar cerrado")
	}
}

func TestWebNeedsARemoteSession(t *testing.T) {
	m := newBridgeManager(t)
	if _, err := m.Tunnel("tab", "no-existe", web.Target{Host: "localhost", Port: 3000}); err == nil {
		t.Fatal("una sesión inexistente debe fallar")
	}
	if _, err := m.Tunnel("tab", "x", web.Target{Host: "localhost"}); err == nil {
		t.Fatal("sin puerto debe fallar")
	}
	if _, err := m.ServeFolder("tab", "no-existe", "/srv"); err == nil {
		t.Fatal("una sesión inexistente debe fallar")
	}
	id := openCmd(t, m) // terminal de este equipo: no hay conexión SSH que usar
	if _, err := m.Tunnel("tab", id, web.Target{Host: "localhost", Port: 3000}); err == nil {
		t.Fatal("una terminal de este equipo no tiene servidor")
	}
	m.CloseWeb("tab") // sin sitios: no hace nada
}
