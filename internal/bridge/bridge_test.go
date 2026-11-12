package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTerms registra lo que le piden. Solo "t1" está compartida.
type fakeTerms struct {
	mu       sync.Mutex
	sent     []string
	lines    int
	command  string
	runs     int
	timeout  time.Duration
	timedOut bool
	notReady error
}

func (f *fakeTerms) SharedTerminals() []Terminal {
	return []Terminal{{ID: "t1", Title: "web-01", Kind: "ssh", Host: "ubuntu@10.0.0.5:22"}}
}

func (f *fakeTerms) check(id string) error {
	if id != "t1" {
		return errors.New("esa terminal no existe o no está compartida")
	}
	return nil
}

func (f *fakeTerms) TerminalText(id string, lines int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lines = lines
	return "ubuntu@web-01:~$ ", f.check(id)
}

func (f *fakeTerms) TerminalSend(id, data string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.check(id); err != nil {
		return err
	}
	f.sent = append(f.sent, data)
	return nil
}

func (f *fakeTerms) TerminalRun(id, command string, timeout time.Duration) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.check(id); err != nil {
		return "", false, err
	}
	f.runs++
	f.command, f.timeout = command, timeout
	return "$ " + command + "\nsalida\n$", f.timedOut, nil
}

func (f *fakeTerms) TerminalReady(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.check(id); err != nil {
		return err
	}
	return f.notReady
}

const token = "secreto-de-prueba"

const (
	what = "Lists the files in the log folder."
	why  = "The user asked which logs exist."
)

func start(t *testing.T, ft *fakeTerms) (*Server, string) {
	t.Helper()
	s := New(ft)
	if err := s.Start(0, token); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	_, port := s.Running()
	return s, fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
}

func post(t *testing.T, url string, body any, mod func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if mod != nil {
		mod(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return resp, buf.Bytes()
}

type reply struct {
	ID     int `json:"id"`
	Result struct {
		ProtocolVersion string                  `json:"protocolVersion"`
		Tools           []map[string]any        `json:"tools"`
		Content         []struct{ Text string } `json:"content"`
		IsError         bool                    `json:"isError"`
	} `json:"result"`
	Error *rpcError `json:"error"`
}

func call(t *testing.T, url, method string, params any) reply {
	t.Helper()
	_, body := post(t, url, map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}, nil)
	var r reply
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("respuesta inválida %q: %v", body, err)
	}
	return r
}

func tool(t *testing.T, url, name string, args any) (text string, isError bool) {
	t.Helper()
	r := call(t, url, "tools/call", map[string]any{"name": name, "arguments": args})
	if r.Error != nil {
		t.Fatalf("error JSON-RPC: %+v", r.Error)
	}
	if len(r.Result.Content) != 1 {
		t.Fatalf("content = %+v", r.Result.Content)
	}
	return r.Result.Content[0].Text, r.Result.IsError
}

func TestRejectsUnauthorizedForeignHostsAndBrowsers(t *testing.T) {
	_, url := start(t, &fakeTerms{})
	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}
	cases := map[string]struct {
		mod  func(*http.Request)
		want int
	}{
		"sin token":     {func(r *http.Request) { r.Header.Del("Authorization") }, 401},
		"token erróneo": {func(r *http.Request) { r.Header.Set("Authorization", "Bearer otro") }, 401},
		"sin Bearer":    {func(r *http.Request) { r.Header.Set("Authorization", token) }, 401},
		"con Origin":    {func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, 403},
		"host ajeno":    {func(r *http.Request) { r.Host = "evil.example" }, 403},
		"ok":            {nil, 200},
	}
	for name, c := range cases {
		if resp, _ := post(t, url, body, c.mod); resp.StatusCode != c.want {
			t.Errorf("%s: status %d, quería %d", name, resp.StatusCode, c.want)
		}
	}
	resp, err := http.Get(url) // GET: sin token
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("GET sin token: %d", resp.StatusCode)
	}
}

func TestInitializeToolsListAndNotifications(t *testing.T) {
	_, url := start(t, &fakeTerms{})
	r := call(t, url, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}})
	if r.Result.ProtocolVersion != "2025-03-26" {
		t.Fatalf("debería aceptar la versión del cliente: %q", r.Result.ProtocolVersion)
	}
	if r = call(t, url, "initialize", map[string]any{"protocolVersion": "1999-01-01"}); r.Result.ProtocolVersion != "2025-06-18" {
		t.Fatalf("con una versión desconocida ofrece la última: %q", r.Result.ProtocolVersion)
	}
	r = call(t, url, "tools/list", nil)
	var names []string
	for _, tl := range r.Result.Tools {
		names = append(names, tl["name"].(string))
		if tl["inputSchema"] == nil || tl["description"] == "" {
			t.Errorf("herramienta incompleta: %v", tl)
		}
	}
	if strings.Join(names, ",") != "list_terminals,read_terminal,run_readonly_command,run_command,send_input,interrupt" {
		t.Fatalf("herramientas: %v", names)
	}
	if r = call(t, url, "no/existe", nil); r.Error == nil || r.Error.Code != -32601 {
		t.Fatalf("método desconocido: %+v", r.Error)
	}
	// Una notificación (sin id) no se responde.
	resp, body := post(t, url, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}, nil)
	if resp.StatusCode != 202 || len(body) != 0 {
		t.Fatalf("notificación: %d %q", resp.StatusCode, body)
	}
}

// Las reglas de permisos de Claude Code se escriben por nombre de herramienta:
// las listas exportadas tienen que coincidir con lo que se ofrece, y cada
// herramienta tiene que declarar si solo lee.
func TestToolsAreSplitIntoReadOnlyAndChanging(t *testing.T) {
	_, url := start(t, &fakeTerms{})
	r := call(t, url, "tools/list", nil)
	got := map[string]map[string]any{}
	for _, tl := range r.Result.Tools {
		got[tl["name"].(string)] = tl
	}
	if len(got) != len(ReadOnlyTools)+len(ChangeTools) {
		t.Fatalf("las listas no coinciden con las herramientas: %v", got)
	}
	for _, n := range ReadOnlyTools {
		if hint := got[n]["annotations"].(map[string]any)["readOnlyHint"]; hint != true {
			t.Errorf("%s debería declararse de solo lectura", n)
		}
	}
	for _, n := range ChangeTools {
		if hint := got[n]["annotations"].(map[string]any)["readOnlyHint"]; hint != false {
			t.Errorf("%s no debería declararse de solo lectura", n)
		}
	}
	// Las que cambian cosas exigen explicar qué hacen y para qué.
	for _, n := range []string{"run_command", "send_input"} {
		req := fmt.Sprint(got[n]["inputSchema"].(map[string]any)["required"])
		if !strings.Contains(req, "what_it_does") || !strings.Contains(req, "why") {
			t.Errorf("%s debería exigir what_it_does y why: %s", n, req)
		}
	}
	if req := fmt.Sprint(got["interrupt"]["inputSchema"].(map[string]any)["required"]); !strings.Contains(req, "why") {
		t.Errorf("interrupt debería exigir why: %s", req)
	}
}

func TestBatch(t *testing.T) {
	_, url := start(t, &fakeTerms{})
	_, body := post(t, url, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "ping"},
		{"jsonrpc": "2.0", "method": "notifications/initialized"},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/list"},
	}, nil)
	var rs []reply
	if err := json.Unmarshal(body, &rs); err != nil || len(rs) != 2 {
		t.Fatalf("lote: %v %q", err, body)
	}
}

func TestTools(t *testing.T) {
	ft := &fakeTerms{}
	_, url := start(t, ft)

	text, isErr := tool(t, url, "list_terminals", nil)
	if isErr || !strings.Contains(text, "id=t1") || !strings.Contains(text, "web-01") || !strings.Contains(text, "ubuntu@10.0.0.5:22") {
		t.Fatalf("list_terminals: %q", text)
	}

	if text, _ = tool(t, url, "read_terminal", map[string]any{"id": "t1"}); text != "ubuntu@web-01:~$ " || ft.lines != 100 {
		t.Fatalf("read_terminal: %q, lines=%d", text, ft.lines)
	}
	tool(t, url, "read_terminal", map[string]any{"id": "t1", "lines": 99999})
	if ft.lines != 2000 {
		t.Fatalf("lines debería topearse en 2000: %d", ft.lines)
	}

	text, isErr = tool(t, url, "run_command", map[string]any{"id": "t1", "command": "uptime", "what_it_does": what, "why": why})
	if isErr || !strings.Contains(text, "salida") || ft.command != "uptime" || ft.timeout != 20*time.Second {
		t.Fatalf("run_command: %q cmd=%q timeout=%v", text, ft.command, ft.timeout)
	}
	tool(t, url, "run_command", map[string]any{"id": "t1", "command": "x", "timeout_seconds": 9999, "what_it_does": what, "why": why})
	if ft.timeout != 120*time.Second {
		t.Fatalf("el tiempo máximo es 120s: %v", ft.timeout)
	}
	ft.timedOut = true
	if text, _ = tool(t, url, "run_command", map[string]any{"id": "t1", "command": "sleep 99", "timeout_seconds": 1, "what_it_does": what, "why": why}); !strings.Contains(text, "timed_out") {
		t.Fatalf("debería avisar que se agotó el tiempo: %q", text)
	}

	tool(t, url, "send_input", map[string]any{"id": "t1", "text": "s", "what_it_does": what, "why": why})
	tool(t, url, "send_input", map[string]any{"id": "t1", "text": "yes", "press_enter": true, "what_it_does": what, "why": why})
	tool(t, url, "interrupt", map[string]any{"id": "t1", "why": why})
	if got := strings.Join(ft.sent, "|"); got != "s|yes\r|\x03" {
		t.Fatalf("enviado: %q", got)
	}
}

// En modo sin permisos no se exige explicar cada comando, y las instrucciones lo dicen.
func TestAutoModeDoesNotRequireExplanations(t *testing.T) {
	ft := &fakeTerms{}
	s, url := start(t, ft)
	args := map[string]any{"id": "t1", "command": "uptime"}

	if _, isErr := tool(t, url, "run_command", args); !isErr {
		t.Fatal("en el modo con permisos run_command sin explicación tiene que fallar")
	}
	s.SetAuto(true)
	if text, isErr := tool(t, url, "run_command", args); isErr || ft.command != "uptime" {
		t.Fatalf("en modo auto debería correr sin explicación: %v %q", isErr, text)
	}
	if _, isErr := tool(t, url, "send_input", map[string]any{"id": "t1", "text": "y"}); isErr {
		t.Fatal("send_input sin explicación en modo auto")
	}
	if _, isErr := tool(t, url, "interrupt", map[string]any{"id": "t1"}); isErr {
		t.Fatal("interrupt sin explicación en modo auto")
	}
	if !strings.Contains(s.instructions(), "without asking") {
		t.Errorf("las instrucciones tienen que decir que no hay que pedir permiso: %q", s.instructions())
	}
	// Una terminal que no está compartida sigue sin verse.
	if _, isErr := tool(t, url, "run_command", map[string]any{"id": "otra", "command": "ls"}); !isErr {
		t.Fatal("el modo auto no cambia qué terminales se ven")
	}
	s.SetAuto(false)
	if _, isErr := tool(t, url, "run_command", args); !isErr {
		t.Fatal("al volver al modo con permisos se exige la explicación otra vez")
	}
}

func TestReadOnlyCommandRunsOnlyWhatItCanVerify(t *testing.T) {
	ft := &fakeTerms{}
	_, url := start(t, ft)

	text, isErr := tool(t, url, "run_readonly_command", map[string]any{"id": "t1", "command": "ps aux | grep nginx | head -5"})
	if isErr || !strings.Contains(text, "salida") || ft.command != "ps aux | grep nginx | head -5" || ft.runs != 1 {
		t.Fatalf("un comando de solo lectura debería correr: err=%v %q cmd=%q runs=%d", isErr, text, ft.command, ft.runs)
	}

	for _, cmd := range []string{"rm -rf /tmp/x", "ls; rm x", "cat /etc/shadow", "echo hi > out", "sudo ls", "systemctl restart nginx"} {
		ft.runs = 0
		text, isErr = tool(t, url, "run_readonly_command", map[string]any{"id": "t1", "command": cmd})
		if !isErr || !strings.Contains(text, "refused") || !strings.Contains(text, "run_command") {
			t.Errorf("%q debería rechazarse e indicar run_command: err=%v %q", cmd, isErr, text)
		}
		if ft.runs != 0 {
			t.Errorf("%q no debería haberse tipeado en la terminal", cmd)
		}
	}
}

func TestReadOnlyCommandNeedsAShellPrompt(t *testing.T) {
	ft := &fakeTerms{notReady: errors.New("the terminal is running a full-screen program")}
	_, url := start(t, ft)
	text, isErr := tool(t, url, "run_readonly_command", map[string]any{"id": "t1", "command": "ls"})
	if !isErr || !strings.Contains(text, "full-screen") || ft.runs != 0 {
		t.Fatalf("si la terminal no está en un prompt no se tipea nada: err=%v %q runs=%d", isErr, text, ft.runs)
	}
}

func TestCommandsThatChangeThingsRequireAnExplanation(t *testing.T) {
	ft := &fakeTerms{}
	_, url := start(t, ft)
	bad := []map[string]any{
		{"id": "t1", "command": "ls"},
		{"id": "t1", "command": "ls", "what_it_does": what},
		{"id": "t1", "command": "ls", "why": why},
		{"id": "t1", "command": "ls", "what_it_does": "ok", "why": "ok"},
		{"id": "t1", "command": "ls", "what_it_does": "          ", "why": "          "},
	}
	for _, args := range bad {
		if text, isErr := tool(t, url, "run_command", args); !isErr || !strings.Contains(text, "what_it_does and why") {
			t.Errorf("run_command %v debería exigir la explicación: %v %q", args, isErr, text)
		}
	}
	if ft.runs != 0 {
		t.Fatal("sin explicación no se tiene que tipear nada")
	}
	if text, isErr := tool(t, url, "send_input", map[string]any{"id": "t1", "text": "yes"}); !isErr || !strings.Contains(text, "what_it_does and why") {
		t.Errorf("send_input sin explicación: %v %q", isErr, text)
	}
	if text, isErr := tool(t, url, "interrupt", map[string]any{"id": "t1"}); !isErr || !strings.Contains(text, "why is required") {
		t.Errorf("interrupt sin motivo: %v %q", isErr, text)
	}
	if len(ft.sent) != 0 {
		t.Fatalf("sin explicación no se tiene que enviar nada: %q", ft.sent)
	}
}

func TestToolErrorsAreReportedAsToolResults(t *testing.T) {
	ft := &fakeTerms{}
	_, url := start(t, ft)
	for name, args := range map[string]map[string]any{
		"read_terminal":        {"id": "ajena"},
		"run_readonly_command": {"id": "ajena", "command": "ls"},
		"run_command":          {"id": "ajena", "command": "ls", "what_it_does": what, "why": why},
		"send_input":           {"id": "ajena", "text": "x", "what_it_does": what, "why": why},
		"interrupt":            {"id": "ajena", "why": why},
	} {
		if text, isErr := tool(t, url, name, args); !isErr || !strings.Contains(text, "no está compartida") {
			t.Errorf("%s con una terminal ajena: isError=%v %q", name, isErr, text)
		}
	}
	if _, isErr := tool(t, url, "run_command", map[string]any{"id": "t1", "command": "  ", "what_it_does": what, "why": why}); !isErr {
		t.Error("un comando vacío debería ser un error")
	}
	r := call(t, url, "tools/call", map[string]any{"name": "rm_rf", "arguments": map[string]any{}})
	if r.Error == nil {
		t.Fatal("una herramienta desconocida es un error del protocolo")
	}
}

func TestStopAndTokenChange(t *testing.T) {
	s, url := start(t, &fakeTerms{})
	s.SetToken("nuevo")
	if resp, _ := post(t, url, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}, nil); resp.StatusCode != 401 {
		t.Fatalf("el token viejo no debería valer: %d", resp.StatusCode)
	}
	if err := s.Start(0, "x"); err == nil {
		t.Fatal("encenderlo dos veces debería fallar")
	}
	s.Stop()
	if on, _ := s.Running(); on {
		t.Fatal("debería estar apagado")
	}
	if _, err := http.Get(url); err == nil {
		t.Fatal("apagado no debería aceptar conexiones")
	}
}
