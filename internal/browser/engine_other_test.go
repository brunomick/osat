//go:build !windows && !(linux && !android && cgo && webkit2_41)

package browser

import (
	"errors"
	"testing"
)

func TestNewUnsupported(t *testing.T) {
	if Supported() {
		t.Fatal("Supported() = true en un sistema sin motor")
	}
	e, err := New(t.TempDir(), func(Event) {})
	if !errors.Is(err, ErrUnsupported) || e != nil {
		t.Fatalf("New = %v, %v; quería nil y ErrUnsupported", e, err)
	}
}
