package remote

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/pkg/sftp"
)

// safeBuffer es un bytes.Buffer que se puede escribir desde varias goroutines.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Una transferencia registra su inicio y su fin (terminada, con error o cancelada).
func TestTransferLogEvents(t *testing.T) {
	var out safeBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&out, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	m := NewManager(nil, nil, func(string, ...interface{}) {})
	waitLog := func(want string) {
		t.Helper()
		waitFor(t, want, func() bool { return strings.Contains(out.String(), want) })
	}

	m.transfers.start(Transfer{Direction: DirUp, Source: "/tmp/a", Target: "/srv/a", Total: 10}, nil, ConflictOverwrite, func(ctx context.Context, _ *sftp.Client, tr *transfer, _ bool) error {
		tr.done.Store(10)
		return nil
	})
	waitLog(`msg="transferencia terminada" dir=up source=/tmp/a target=/srv/a bytes=10`)

	m.transfers.start(Transfer{Direction: DirDown, Source: "/srv/b", Target: "/tmp/b"}, nil, ConflictOverwrite, func(ctx context.Context, _ *sftp.Client, tr *transfer, _ bool) error {
		return errors.New("disco lleno")
	})
	waitLog(`msg="transferencia con error"`)
	waitLog(`err="disco lleno"`)

	id := m.transfers.start(Transfer{Direction: DirUp, Source: "/tmp/c", Target: "/srv/c"}, nil, ConflictOverwrite, func(ctx context.Context, _ *sftp.Client, tr *transfer, _ bool) error {
		<-ctx.Done()
		return ctx.Err()
	})
	waitLog(`msg="transferencia iniciada" dir=up source=/tmp/c`)
	m.CancelTransfer(id)
	waitLog(`msg="transferencia cancelada" dir=up source=/tmp/c`)

	if !strings.Contains(out.String(), "duration=") {
		t.Errorf("falta la duración:\n%s", out.String())
	}
}
