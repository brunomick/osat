// Package bridge es un servidor MCP (Model Context Protocol) local para que
// Claude Code pueda ver y manejar las terminales de osat que el usuario eligió
// compartir. Habla MCP por HTTP en 127.0.0.1, protegido con un token, y solo
// expone lo que implemente Terminals (osat solo le pasa las compartidas).
package bridge

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"osat/internal/readonly"
)

// Terminal es una terminal compartida.
type Terminal struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"` // "ssh" o "local"
	Host  string `json:"host,omitempty"`
}

// Terminals es lo que el puente puede hacer con las terminales. Cualquier id
// que no esté compartida tiene que dar error.
type Terminals interface {
	SharedTerminals() []Terminal
	// TerminalText devuelve las últimas lines líneas que se ven (con historial).
	TerminalText(id string, lines int) (string, error)
	// TerminalSend escribe tal cual en la terminal (como si se tipeara).
	TerminalSend(id, data string) error
	// TerminalRun tipea command + Enter y espera a que la salida se asiente.
	TerminalRun(id, command string, timeout time.Duration) (output string, timedOut bool, err error)
	// TerminalReady dice si la terminal está en un prompt de shell (no dentro de vim, less, un cliente de base de datos...).
	TerminalReady(id string) error
}

// Server es el servidor MCP.
type Server struct {
	terms Terminals
	// auto: el usuario dejó que Claude corra lo que quiera sin pedirle permiso
	// (ver SetAuto). Cambia lo que se le dice al modelo y que no se le exija
	// explicar cada comando; el permiso en sí lo pide (o no) Claude Code.
	auto atomic.Bool

	mu    sync.Mutex
	srv   *http.Server
	token string
	port  int
}

func New(t Terminals) *Server { return &Server{terms: t} }

// SetAuto activa o desactiva el modo sin permisos: las herramientas que cambian
// cosas dejan de exigir la explicación (nadie la va a leer en un cartel de
// aprobación) y las instrucciones lo dicen.
func (s *Server) SetAuto(on bool) { s.auto.Store(on) }

// instructions es lo que ve el modelo al conectarse.
func (s *Server) instructions() string {
	const head = "These are terminals the user opened in osat and explicitly shared with you; they may be production servers or shells on the user's own PC. Use list_terminals first. "
	const tail = "To drive an interactive program running in a terminal (a TUI such as opencode, vim or a database client), use read_terminal to see its screen and send_input to type into it."
	if s.auto.Load() {
		return head + "The user has allowed you to run anything in them without asking each time, so run_command, send_input and interrupt do not need approval or an explanation. " +
			"That does not widen what you were asked to do: only do what the user asked for, and be careful with anything destructive. " + tail
	}
	return head + "To inspect, use run_readonly_command (no approval needed; osat only accepts commands it can verify are read-only). " +
		"run_command, send_input and interrupt change things and the user is asked to approve each one: before calling them, explain in your message what the command does and why, and fill in what_it_does and why. Only do what the user asked for. " + tail
}

// Start empieza a escuchar en 127.0.0.1:port (solo esta PC). Devuelve error si
// el puerto está ocupado.
func (s *Server) Start(port int, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.srv != nil {
		return errors.New("el puente ya está encendido")
	}
	if token == "" {
		return errors.New("falta el token")
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	s.token, s.port = token, ln.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: http.HandlerFunc(s.serve), ReadHeaderTimeout: 10 * time.Second}
	s.srv = srv
	go srv.Serve(ln)
	return nil
}

// Stop deja de escuchar y corta las conexiones.
func (s *Server) Stop() {
	s.mu.Lock()
	srv := s.srv
	s.srv = nil
	s.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if srv.Shutdown(ctx) != nil {
			srv.Close()
		}
	}
}

// Running dice si está escuchando, y en qué puerto.
func (s *Server) Running() (bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.srv != nil, s.port
}

// SetToken cambia el token: las conexiones con el anterior dejan de valer.
func (s *Server) SetToken(token string) {
	s.mu.Lock()
	s.token = token
	s.mu.Unlock()
}

// ---------- HTTP ----------

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	// Solo desde esta PC y sin páginas web de por medio (DNS rebinding / CSRF).
	if !loopbackHost(r.Host) || r.Header.Get("Origin") != "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.mu.Lock()
	token := s.token
	s.mu.Unlock()
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == r.Header.Get("Authorization") || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="osat"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.URL.Path != "/mcp" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	resp, isBatch := s.handleBody(body)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted) // solo notificaciones
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var out []byte
	if isBatch {
		out, _ = json.Marshal(resp)
	} else {
		out, _ = json.Marshal(resp[0])
	}
	w.Write(out)
}

func loopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// ---------- JSON-RPC ----------

type rpcRequest struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// handleBody devuelve las respuestas a enviar (nil si eran solo notificaciones).
func (s *Server) handleBody(body []byte) (resps []rpcResponse, batch bool) {
	trimmed := strings.TrimSpace(string(body))
	var reqs []rpcRequest
	if strings.HasPrefix(trimmed, "[") {
		batch = true
		if err := json.Unmarshal(body, &reqs); err != nil {
			return []rpcResponse{errResp(nil, -32700, "JSON inválido")}, false
		}
	} else {
		var one rpcRequest
		if err := json.Unmarshal(body, &one); err != nil {
			return []rpcResponse{errResp(nil, -32700, "JSON inválido")}, false
		}
		reqs = []rpcRequest{one}
	}
	for _, rq := range reqs {
		if len(rq.ID) == 0 || string(rq.ID) == "null" {
			continue // notificación: no se responde
		}
		resps = append(resps, s.dispatch(rq))
	}
	return resps, batch
}

func errResp(id json.RawMessage, code int, msg string) rpcResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

func (s *Server) dispatch(rq rpcRequest) rpcResponse {
	ok := func(result any) rpcResponse { return rpcResponse{JSONRPC: "2.0", ID: rq.ID, Result: result} }
	switch rq.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(rq.Params, &p)
		version := "2025-06-18"
		for _, v := range []string{"2025-06-18", "2025-03-26", "2024-11-05"} {
			if p.ProtocolVersion == v {
				version = v
			}
		}
		return ok(map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "osat", "version": "0.1.0"},
			"instructions":    s.instructions(),
		})
	case "ping":
		return ok(map[string]any{})
	case "tools/list":
		return ok(map[string]any{"tools": toolDefs})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(rq.Params, &p); err != nil {
			return errResp(rq.ID, -32602, "parámetros inválidos")
		}
		text, err := s.callTool(p.Name, p.Arguments)
		if errors.Is(err, errUnknownTool) {
			return errResp(rq.ID, -32602, err.Error())
		}
		if err != nil {
			return ok(toolResult("Error: "+err.Error(), true))
		}
		return ok(toolResult(text, false))
	}
	return errResp(rq.ID, -32601, "método desconocido: "+rq.Method)
}

func toolResult(text string, isError bool) map[string]any {
	r := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
	if isError {
		r["isError"] = true
	}
	return r
}

// ---------- herramientas ----------

var errUnknownTool = errors.New("herramienta desconocida")

type obj = map[string]any

var idProp = obj{"type": "string", "description": "Terminal id from list_terminals."}

// readOnlyTools y changeTools son los nombres que Claude Code usa en sus reglas
// de permisos (mcp__osat__<nombre>): las de solo lectura se pueden permitir de
// una vez; las demás conviene dejarlas en "ask".
var (
	ReadOnlyTools = []string{"list_terminals", "read_terminal", "run_readonly_command"}
	ChangeTools   = []string{"run_command", "send_input", "interrupt"}
)

// ReadOnlyHint / change hints (MCP annotations).
var (
	hintRead   = obj{"readOnlyHint": true, "openWorldHint": false}
	hintChange = obj{"readOnlyHint": false, "destructiveHint": true, "openWorldHint": false}
)

var explainProps = obj{
	"what_it_does": obj{"type": "string", "description": "Plain-language explanation, for the user, of what this does and what it changes. It is shown in the approval prompt."},
	"why":          obj{"type": "string", "description": "Why you need to do this for what the user asked. It is shown in the approval prompt."},
}

func props(extra obj, withExplain bool) obj {
	p := obj{"id": idProp}
	for k, v := range extra {
		p[k] = v
	}
	if withExplain {
		for k, v := range explainProps {
			p[k] = v
		}
	}
	return p
}

var toolDefs = []obj{
	{
		"name":        "list_terminals",
		"description": "List the osat terminals the user has shared with you (SSH sessions and local shells). Other terminals are not visible.",
		"inputSchema": obj{"type": "object", "properties": obj{}},
		"annotations": hintRead,
	},
	{
		"name":        "read_terminal",
		"description": "Read what a shared terminal currently shows, including recent scrollback, as plain text (colors removed). Full-screen programs such as vim or top only show their current screen.",
		"inputSchema": obj{
			"type":     "object",
			"required": []string{"id"},
			"properties": props(obj{
				"lines": obj{"type": "integer", "description": "How many of the last lines to return (default 100, max 2000)."},
			}, false),
		},
		"annotations": hintRead,
	},
	{
		"name": "run_readonly_command",
		"description": "Run a command in a shared terminal WITHOUT asking the user, but only if osat can verify it is read-only (it inspects state and never changes it): ls, cat, head, tail (no -f), grep, find (no -exec/-delete), ps, df, free, uname, ip addr, ss, " +
			"docker ps/logs/inspect, kubectl get/describe, `git --no-pager log|status|diff`, `systemctl --no-pager status`, `journalctl --no-pager`, and pipes between such commands. " +
			"Refused: ; & && || redirects (> <), $ or backticks, sudo, commands given by path, and anything that may expose secrets (keys, passwords, tokens, .env). " +
			"If this refuses, do NOT try to work around it: explain to the user what you want to run and use run_command, which asks for approval. " +
			"Returns everything printed since the command (the command line, its output and the next prompt). The terminal must be at a shell prompt.",
		"inputSchema": obj{
			"type":     "object",
			"required": []string{"id", "command"},
			"properties": props(obj{
				"command":         obj{"type": "string", "description": "A single read-only command or a pipeline of them."},
				"timeout_seconds": obj{"type": "integer", "description": "How long to wait for the output (default 20, max 120)."},
			}, false),
		},
		"annotations": hintRead,
	},
	{
		"name": "run_command",
		"description": "Type ANY command into a shared terminal, press Enter and return what it printed. The user is asked to approve every call, so FIRST explain in your reply what the command does and why, and fill in what_it_does and why (they appear in the approval prompt). " +
			"Prefer run_readonly_command for anything that only inspects. If timed_out is reported the command is probably still running: use read_terminal to follow it, or interrupt to stop it.",
		"inputSchema": obj{
			"type":     "object",
			"required": []string{"id", "command", "what_it_does", "why"},
			"properties": props(obj{
				"command":         obj{"type": "string", "description": "The command line to run. It is typed followed by Enter."},
				"timeout_seconds": obj{"type": "integer", "description": "How long to wait for the output (default 20, max 120)."},
			}, true),
		},
		"annotations": hintChange,
	},
	{
		"name": "send_input",
		"description": "Type raw text into a shared terminal without waiting, for answering prompts or driving interactive programs. Enter is not pressed unless press_enter is true. Control keys are sent as their characters (Esc is \u001b, Tab is \t, Up arrow is \u001b[A, Ctrl+D is \u0004). " +
			"The user is asked to approve every call: explain what you are typing and why, and fill in what_it_does and why. Use read_terminal afterwards to see the result.",
		"inputSchema": obj{
			"type":     "object",
			"required": []string{"id", "text", "what_it_does", "why"},
			"properties": props(obj{
				"text":        obj{"type": "string", "description": "Text to type."},
				"press_enter": obj{"type": "boolean", "description": "Press Enter after the text (default false)."},
			}, true),
		},
		"annotations": hintChange,
	},
	{
		"name":        "interrupt",
		"description": "Press Ctrl+C in a shared terminal to stop the running command. The user is asked to approve it: say what you are stopping and fill in why.",
		"inputSchema": obj{
			"type":     "object",
			"required": []string{"id", "why"},
			"properties": props(obj{
				"why": explainProps["why"],
			}, false),
		},
		"annotations": hintChange,
	},
}

// minExplain es el largo mínimo (sin espacios de los costados) de las
// explicaciones: evita que se rellenen con una palabra suelta.
const minExplain = 10

func (s *Server) needExplain(what, why string) error {
	if s.auto.Load() {
		return nil
	}
	if len(strings.TrimSpace(what)) < minExplain || len(strings.TrimSpace(why)) < minExplain {
		return errors.New("what_it_does and why are required: explain to the user in plain language what this does and why it is needed (a full sentence each); they are shown in the approval prompt")
	}
	return nil
}

func (s *Server) callTool(name string, raw json.RawMessage) (string, error) {
	var a struct {
		ID             string `json:"id"`
		Lines          int    `json:"lines"`
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		Text           string `json:"text"`
		PressEnter     bool   `json:"press_enter"`
		WhatItDoes     string `json:"what_it_does"`
		Why            string `json:"why"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", fmt.Errorf("argumentos inválidos: %w", err)
		}
	}
	run := func() (string, error) {
		if strings.TrimSpace(a.Command) == "" {
			return "", errors.New("command is empty")
		}
		secs := a.TimeoutSeconds
		if secs <= 0 {
			secs = 20
		}
		secs = min(secs, 120)
		out, timedOut, err := s.terms.TerminalRun(a.ID, a.Command, time.Duration(secs)*time.Second)
		if err != nil {
			return "", err
		}
		if timedOut {
			out += "\n\n[timed_out: the output had not settled after " + strconv.Itoa(secs) + "s; the command may still be running]"
		}
		return out, nil
	}
	switch name {
	case "list_terminals":
		list := s.terms.SharedTerminals()
		if len(list) == 0 {
			return "No terminals are shared. Ask the user to enable sharing on a terminal tab in osat.", nil
		}
		var b strings.Builder
		for _, t := range list {
			fmt.Fprintf(&b, "id=%s  %s  (%s", t.ID, t.Title, t.Kind)
			if t.Host != "" {
				fmt.Fprintf(&b, ", %s", t.Host)
			}
			b.WriteString(")\n")
		}
		return strings.TrimRight(b.String(), "\n"), nil
	case "read_terminal":
		n := a.Lines
		if n <= 0 {
			n = 100
		}
		n = min(n, 2000)
		text, err := s.terms.TerminalText(a.ID, n)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) == "" {
			return "(the terminal is empty)", nil
		}
		return text, nil
	case "run_readonly_command":
		// La verificación la hace osat, no el modelo: si no puede probar que es de
		// solo lectura, se rechaza y hay que usar run_command (que pide permiso).
		if err := readonly.Check(a.Command); err != nil {
			return "", fmt.Errorf("refused, not recognized as read-only: %w. If you really need it, explain to the user what it does and why, and use run_command (the user will be asked to approve)", err)
		}
		// Y solo se tipea si la terminal está en un prompt de shell: si hubiera un
		// editor o un cliente de base de datos, el texto iría a parar ahí.
		if err := s.terms.TerminalReady(a.ID); err != nil {
			return "", err
		}
		return run()
	case "run_command":
		if err := s.needExplain(a.WhatItDoes, a.Why); err != nil {
			return "", err
		}
		return run()
	case "send_input":
		if err := s.needExplain(a.WhatItDoes, a.Why); err != nil {
			return "", err
		}
		data := a.Text
		if a.PressEnter {
			data += "\r"
		}
		if err := s.terms.TerminalSend(a.ID, data); err != nil {
			return "", err
		}
		return "sent", nil
	case "interrupt":
		if !s.auto.Load() && len(strings.TrimSpace(a.Why)) < minExplain {
			return "", errors.New("why is required: tell the user in a full sentence what you are stopping and why")
		}
		if err := s.terms.TerminalSend(a.ID, "\x03"); err != nil {
			return "", err
		}
		return "sent Ctrl+C", nil
	}
	return "", errUnknownTool
}
