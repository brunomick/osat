package remote

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseTmuxSessions(t *testing.T) {
	out := "$1\tmanual\t0\t1795615635\t1\t2\t\n" +
		"$0\tmi shell\t2\t1795615600\t1\t1\tbruno@PC\n" +
		"\n" +
		"basura sin tabs\n"
	got := parseTmuxSessions(out)
	want := []TmuxSession{
		{ID: "$1", Name: "manual", Attached: 0, Activity: 1795615635, Windows: 1, Panes: 2},
		{ID: "$0", Name: "mi shell", Attached: 2, Activity: 1795615600, Windows: 1, Panes: 1, Owner: "bruno@PC"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseTmuxVersion(t *testing.T) {
	cases := []struct {
		in           string
		major, minor int
		ok           bool
	}{
		{"tmux 3.4\n", 3, 4, true},
		{"tmux 3.0a", 3, 0, true},
		{"tmux next-3.5", 3, 5, true},
		{"tmux 2.7", 2, 7, true},
		{"tmux master", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, c := range cases {
		ma, mi, ok := parseTmuxVersion(c.in)
		if ma != c.major || mi != c.minor || ok != c.ok {
			t.Errorf("%q: got %d.%d %v", c.in, ma, mi, ok)
		}
	}
}

func TestSendKeysCommandsChunks(t *testing.T) {
	if got := sendKeysCommands("%3", []byte("ls\r")); !reflect.DeepEqual(got, []string{"send-keys -t %3 -H 6c 73 0d"}) {
		t.Fatalf("got %q", got)
	}
	big := make([]byte, 1300)
	cmds := sendKeysCommands("%0", big)
	if len(cmds) != 3 {
		t.Fatalf("got %d commands", len(cmds))
	}
	total := 0
	for _, c := range cmds {
		total += len(strings.Fields(c)) - 4
	}
	if total != 1300 {
		t.Fatalf("total bytes %d", total)
	}
	if sendKeysCommands("%0", nil) != nil {
		t.Fatal("empty input must send nothing")
	}
}

func TestDecodeOctalKeepsPartialUTF8(t *testing.T) {
	// "ñ" es \303\261: si tmux lo parte entre dos %output, cada mitad tiene que pasar tal cual.
	if got := decodeOctal([]byte(`a\303`)); !reflect.DeepEqual(got, []byte{'a', 0xc3}) {
		t.Fatalf("got %v", got)
	}
	if got := decodeOctal([]byte(`\261b\134\015`)); !reflect.DeepEqual(got, []byte{0xb1, 'b', '\\', '\r'}) {
		t.Fatalf("got %v", got)
	}
	// Una barra que no es un escape completo queda como está.
	if got := decodeOctal([]byte(`x\9`)); string(got) != `x\9` {
		t.Fatalf("got %q", got)
	}
}

func TestControlParserBlocksAndNotifications(t *testing.T) {
	var p controlParser
	var replies []controlReply
	var notes []string
	p.onReply = func(r controlReply) { replies = append(replies, r) }
	p.onNote = func(name string, args []byte) { notes = append(notes, name+"|"+string(args)) }
	for _, l := range []string{
		"%begin 1795615635 282 0",
		"%end 1795615635 282 0",
		"%session-changed $0 mi shell",
		"%begin 1795615635 287 1",
		"%0 59 3",
		"%output %0 dentro de un bloque no es notificación",
		"%end 1795615635 287 1",
		"%output %0 hola\\015",
		"%begin 1 288 1",
		"can't find session",
		"%error 1 288 1",
		"%session-renamed $0 otro",
		"%exit",
	} {
		p.feed([]byte(l))
	}
	// El primer bloque (flags 0) es el del propio attach, no una respuesta a un comando nuestro: se descarta.
	wantReplies := []controlReply{
		{lines: []string{"%0 59 3", "%output %0 dentro de un bloque no es notificación"}},
		{lines: []string{"can't find session"}, err: true},
	}
	if !reflect.DeepEqual(replies, wantReplies) {
		t.Fatalf("replies %+v", replies)
	}
	wantNotes := []string{"session-changed|$0 mi shell", "output|%0 hola\\015", "session-renamed|$0 otro", "exit|"}
	if !reflect.DeepEqual(notes, wantNotes) {
		t.Fatalf("notes %q", notes)
	}
}

func TestTmuxCommands(t *testing.T) {
	if got := tmuxListScript(); !strings.HasPrefix(got, "command -v tmux >/dev/null 2>&1 || exit 127; tmux -V; tmux list-sessions -F '#{session_id}\t") || !strings.HasSuffix(got, "' 2>/dev/null; exit 0") {
		t.Fatalf("list: %q", got)
	}
	if got := tmuxRenameScript("$3", "it's mine"); got != `tmux rename-session -t '$3' -- 'it'\''s mine' && tmux display-message -p -t '$3' '#{session_name}'` {
		t.Fatalf("rename: %q", got)
	}
	if got := tmuxKillScript("$3"); got != `tmux kill-session -t '$3'` {
		t.Fatalf("kill: %q", got)
	}
	got := tmuxCreateScript("web", []string{"A=b c"}, true)
	want := `tmux new-session -d -P -F '` + tmuxListFormat + `' -s 'web' -e 'A=b c'`
	if got != want {
		t.Fatalf("create:\n%q\n%q", got, want)
	}
	// Sin -e (tmux < 3.2) las variables no se pasan; nombre vacío = tmux elige uno.
	if got := tmuxCreateScript("", []string{"A=b"}, false); got != `tmux new-session -d -P -F '`+tmuxListFormat+`'` {
		t.Fatalf("create sin nombre: %q", got)
	}
}

func TestTmuxOwner(t *testing.T) {
	if got := tmuxOwnerFrom(`DOMINIO\bruno.mick`, "PC-BRUNO"); got != "bruno.mick@PC-BRUNO" {
		t.Fatalf("got %q", got)
	}
	if got := tmuxOwnerFrom("", ""); got != "osat" {
		t.Fatalf("got %q", got)
	}
}

func TestParseTmuxList(t *testing.T) {
	l, err := parseTmuxList("tmux 3.0a\n$0\tweb\t1\t5\t1\t1\tbruno@PC\n")
	if err != nil || l.Version != "3.0a" || l.ReadOnly || len(l.Sessions) != 1 {
		t.Fatalf("got %+v %v", l, err)
	}
	if l, _ := parseTmuxList("tmux 3.4\n"); !l.ReadOnly || len(l.Sessions) != 0 {
		t.Fatalf("got %+v", l)
	}
	if _, err := parseTmuxList("tmux 2.7\n"); err == nil {
		t.Fatal("tmux 2.7 must be rejected")
	}
}
