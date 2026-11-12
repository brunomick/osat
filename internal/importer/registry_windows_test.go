//go:build windows

package importer

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

// Lee sesiones del registro real con el mismo formato que PuTTY, pero en una
// clave propia y temporal (no toca las de PuTTY ni WinSCP).
func TestReadRegistry(t *testing.T) {
	base := fmt.Sprintf(`Software\osat-test-%d`, time.Now().UnixNano())
	t.Cleanup(func() {
		registry.DeleteKey(registry.CURRENT_USER, base+`\AWS%20web`)
		registry.DeleteKey(registry.CURRENT_USER, base)
	})
	if _, err := readRegistry(base); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clave inexistente: err = %v", err)
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, base+`\AWS%20web`, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	k.SetStringValue("HostName", "ubuntu@10.0.0.5")
	k.SetDWordValue("PortNumber", 2222)
	k.SetStringValue("Protocol", "ssh")
	k.Close()

	raw, err := readRegistry(base)
	if err != nil {
		t.Fatal(err)
	}
	items, skipped := puttyItems(raw)
	if len(items) != 1 || len(skipped) != 0 {
		t.Fatalf("items %+v, omitidas %v", items, skipped)
	}
	if it := items[0]; it.Name != "AWS web" || it.Host != "10.0.0.5" || it.User != "ubuntu" || it.Port != 2222 {
		t.Errorf("sesión = %+v", it)
	}
}
