//go:build windows

package applock

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// La PC de pruebas puede tener o no Windows Hello: solo se comprueba que no entra en pánico.
func TestHelloAvailableNoPanic(t *testing.T) {
	t.Logf("HelloAvailable() = %v", HelloAvailable())
}

// pinterfaceIID tiene que dar los IID que publica Windows para interfaces genéricas conocidas.
func TestPinterfaceIID(t *testing.T) {
	cases := []struct{ generic, arg, want string }{
		{"913337e9-11a1-4345-a3a2-4e7f956e222d", "string", "{98B9ACC1-4B56-532E-AC73-03D5291CCA90}"}, // IVector<String>
		{"9fc2b0bb-e446-44e2-aa61-9cab8f636af2", "enum(Windows.Security.Credentials.UI.UserConsentVerificationResult;i4)",
			"{FD596FFD-2318-558F-9DBE-D21DF43764A5}"}, // IAsyncOperation<UserConsentVerificationResult>
	}
	for _, c := range cases {
		got := pinterfaceIID(c.generic, c.arg).String()
		if !strings.EqualFold(got, c.want) {
			t.Errorf("pinterfaceIID(%s) = %s, quería %s", c.arg, got, c.want)
		}
	}
}

// La ventana dueña del diálogo de Hello es propia de osat: se crea, queda habilitada y se destruye
// sin tocar la ventana principal (ver ownerWindow).
func TestHelloOwnerWindow(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	h, err := ownerWindow(0)
	if err != nil || h == 0 {
		t.Fatalf("ownerWindow: %v", err)
	}
	pumpFor(20 * time.Millisecond)
	if r, _, _ := procDestroyWindow.Call(h); r == 0 {
		t.Fatal("no se pudo destruir la ventana dueña")
	}
}
