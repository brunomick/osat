package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"osat/internal/i18n"
)

// Kubernetes con el kubectl del servidor: se listan namespaces, pods y servicios,
// y un reenvío a un pod o servicio corre "kubectl port-forward" en el servidor
// (por SSH), cuyo puerto temporal se trae a esta PC como cualquier otro puerto.
// Todo lo que va a la línea de comandos pasó antes por store.Forward.Validate.

var errNoKubectl = i18n.Err("remote.kubectlIsNotInstalledOn")

const kubectlTimeout = 30 * time.Second

// withClient presta la conexión SSH de una sesión mientras dura fn.
func (m *Manager) withClient(sessionID string, fn func(*ssh.Client) error) error {
	src, err := m.get(sessionID)
	if err != nil {
		return err
	}
	if src.shared == nil {
		return errNotRemote
	}
	if err := src.shared.acquire(); err != nil {
		return err
	}
	defer src.shared.release()
	return fn(src.shared.client)
}

// kubectlOut corre un comando en el servidor y devuelve lo que imprimió.
func kubectlOut(ctx context.Context, client *ssh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var out, errOut bytes.Buffer
	sess.Stdout, sess.Stderr = &out, &errOut
	if err := sess.Start(cmd); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		return "", i18n.Err("remote.kubectlTookTooLongTo")
	}
	if err != nil {
		var ee *ssh.ExitError
		if errors.As(err, &ee) && ee.ExitStatus() == 127 {
			return "", errNoKubectl
		}
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			return "", errors.New(shorten(msg))
		}
		return "", err
	}
	return out.String(), nil
}

func shorten(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// K8sNamespaces son los namespaces del cluster y el que usa por defecto el kubectl del servidor.
type K8sNamespaces struct {
	Names   []string `json:"names"`
	Current string   `json:"current"`
}

// K8sPort es un puerto de un pod (containerPort) o de un servicio.
type K8sPort struct {
	Port int    `json:"port"`
	Name string `json:"name,omitempty"`
}

// K8sItem es un pod o un servicio.
type K8sItem struct {
	Kind   string    `json:"kind"` // "pod" o "svc"
	Name   string    `json:"name"`
	Status string    `json:"status"` // pods: Running, Pending, CrashLoopBackOff...; servicios: ClusterIP, NodePort...
	Ports  []K8sPort `json:"ports"`
}

func (m *Manager) K8sNamespaces(sessionID string) (K8sNamespaces, error) {
	var res K8sNamespaces
	err := m.withClient(sessionID, func(c *ssh.Client) error {
		ctx, cancel := context.WithTimeout(context.Background(), kubectlTimeout)
		defer cancel()
		// El namespace actual se pide aparte: si falla, no importa.
		if cur, err := kubectlOut(ctx, c, `kubectl config view --minify -o jsonpath='{..namespace}'`); err == nil {
			res.Current = strings.TrimSpace(cur)
		}
		if res.Current == "" {
			res.Current = "default"
		}
		out, err := kubectlOut(ctx, c, "kubectl get namespaces -o json")
		if err != nil {
			return err
		}
		res.Names, err = parseNamespaces(out)
		return err
	})
	return res, err
}

func (m *Manager) K8sResources(sessionID, namespace string) ([]K8sItem, error) {
	if !validNamespace(namespace) {
		return nil, i18n.Err("remote.invalidNamespaceQ", namespace)
	}
	var items []K8sItem
	err := m.withClient(sessionID, func(c *ssh.Client) error {
		ctx, cancel := context.WithTimeout(context.Background(), kubectlTimeout)
		defer cancel()
		out, err := kubectlOut(ctx, c, "kubectl get pods,services -n "+namespace+" -o json")
		if err != nil {
			return err
		}
		items, err = parseResources(out)
		return err
	})
	return items, err
}

var nsName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func validNamespace(s string) bool { return len(s) <= 63 && nsName.MatchString(s) }

type k8sList struct {
	Items []struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			Type       string `json:"type"`
			Containers []struct {
				Ports []struct {
					Name          string `json:"name"`
					ContainerPort int    `json:"containerPort"`
					Protocol      string `json:"protocol"`
				} `json:"ports"`
			} `json:"containers"`
			Ports []struct {
				Name     string `json:"name"`
				Port     int    `json:"port"`
				Protocol string `json:"protocol"`
			} `json:"ports"`
		} `json:"spec"`
		Status struct {
			Phase             string `json:"phase"`
			ContainerStatuses []struct {
				State struct {
					Waiting *struct {
						Reason string `json:"reason"`
					} `json:"waiting"`
				} `json:"state"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

func parseNamespaces(raw string) ([]string, error) {
	var l k8sList
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		return nil, i18n.Err("remote.kubectlReturnedSomethingThatCan", err)
	}
	out := make([]string, 0, len(l.Items))
	for _, it := range l.Items {
		out = append(out, it.Metadata.Name)
	}
	sort.Strings(out)
	return out, nil
}

func parseResources(raw string) ([]K8sItem, error) {
	var l k8sList
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		return nil, i18n.Err("remote.kubectlReturnedSomethingThatCan", err)
	}
	var items []K8sItem
	for _, it := range l.Items {
		switch it.Kind {
		case "Pod":
			p := K8sItem{Kind: "pod", Name: it.Metadata.Name, Status: it.Status.Phase, Ports: []K8sPort{}}
			for _, cs := range it.Status.ContainerStatuses { // CrashLoopBackOff, ImagePullBackOff...
				if w := cs.State.Waiting; w != nil && w.Reason != "" {
					p.Status = w.Reason
				}
			}
			for _, c := range it.Spec.Containers {
				for _, cp := range c.Ports {
					if cp.Protocol == "" || cp.Protocol == "TCP" {
						p.Ports = append(p.Ports, K8sPort{Port: cp.ContainerPort, Name: cp.Name})
					}
				}
			}
			items = append(items, p)
		case "Service":
			s := K8sItem{Kind: "svc", Name: it.Metadata.Name, Status: it.Spec.Type, Ports: []K8sPort{}}
			for _, sp := range it.Spec.Ports {
				if sp.Protocol == "" || sp.Protocol == "TCP" {
					s.Ports = append(s.Ports, K8sPort{Port: sp.Port, Name: sp.Name})
				}
			}
			items = append(items, s)
		}
	}
	// Servicios primero (lo que se suele querer), cada grupo por nombre.
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind == "svc"
		}
		return items[i].Name < items[j].Name
	})
	return items, nil
}

// kubePF es un "kubectl port-forward" corriendo en el servidor.
type kubePF struct {
	sess  *ssh.Session
	stdin io.WriteCloser
	done  chan struct{} // se cierra cuando kubectl terminó

	mu   sync.Mutex
	tail []string // últimas líneas que imprimió
	err  error
}

var (
	forwardingFrom = regexp.MustCompile(`Forwarding from 127\.0\.0\.1:(\d+) ->`)
	ansi           = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
)

// startPortForward lanza kubectl y espera a que diga en qué puerto (del servidor) quedó escuchando.
// El puerto lo elige kubectl (":PUERTO"), así no choca con nada del servidor.
func startPortForward(ctx context.Context, client *ssh.Client, namespace, resource string, port int) (*kubePF, int, error) {
	sess, err := client.NewSession()
	if err != nil {
		return nil, 0, err
	}
	// Con una PTY, al cortarse la conexión o cerrar la sesión el servidor le avisa a kubectl que termine
	// (sin ella, kubectl podría quedar corriendo en el servidor).
	if err := sess.RequestPty("xterm", 24, 200, ssh.TerminalModes{ssh.ECHO: 0}); err != nil {
		sess.Close()
		return nil, 0, i18n.Err("remote.couldnTPrepareTheCommand", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, 0, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, 0, err
	}
	cmd := fmt.Sprintf("kubectl port-forward -n %s %s :%d --address 127.0.0.1", namespace, resource, port)
	if err := sess.Start(cmd); err != nil {
		sess.Close()
		return nil, 0, err
	}
	p := &kubePF{sess: sess, stdin: stdin, done: make(chan struct{})}
	ready := make(chan int, 1)
	var readers sync.WaitGroup
	readers.Add(1)
	go func() { // hay que seguir leyendo: kubectl escribe una línea por conexión
		defer readers.Done()
		var buf []byte
		chunk := make([]byte, 4096)
		for {
			n, err := stdout.Read(chunk)
			buf = append(buf, chunk[:n]...)
			for {
				i := bytes.IndexByte(buf, '\n')
				if i < 0 {
					break
				}
				line := strings.TrimSpace(ansi.ReplaceAllString(string(buf[:i]), ""))
				buf = buf[i+1:]
				if line == "" {
					continue
				}
				p.addLine(line)
				if m := forwardingFrom.FindStringSubmatch(line); m != nil {
					if v, _ := strconv.Atoi(m[1]); v > 0 {
						select {
						case ready <- v:
						default:
						}
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		err := sess.Wait()
		readers.Wait()
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		close(p.done)
	}()

	select {
	case v := <-ready:
		return p, v, nil
	case <-p.done:
		return nil, 0, p.failure()
	case <-ctx.Done():
		p.stop()
		return nil, 0, i18n.Err("remote.cancelled")
	case <-time.After(25 * time.Second):
		p.stop()
		return nil, 0, i18n.Err("remote.kubectlDidnTManageTo")
	}
}

func (p *kubePF) addLine(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tail = append(p.tail, s)
	if len(p.tail) > 6 {
		p.tail = p.tail[1:]
	}
}

// failure explica por qué kubectl terminó, con lo último que dijo.
func (p *kubePF) failure() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	said := strings.Join(p.tail, " ")
	var ee *ssh.ExitError
	if errors.As(p.err, &ee) && ee.ExitStatus() == 127 || strings.Contains(said, "command not found") {
		return errNoKubectl
	}
	if said == "" {
		return i18n.Err("remote.kubectlEndedWithoutSayingWhy")
	}
	return errors.New(shorten(said))
}

// stop le avisa a kubectl (Ctrl+C) y cierra la sesión.
func (p *kubePF) stop() {
	_, _ = p.stdin.Write([]byte{3})
	p.sess.Close()
}
