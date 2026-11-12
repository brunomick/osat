package applock

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// fakeStore es un almacén en memoria; failSet hace que guardar falle y getErr, que leer falle.
type fakeStore struct {
	m       map[string]string
	failSet bool
	getErr  error
}

func newFakeStore() *fakeStore { return &fakeStore{m: map[string]string{}} }

func (f *fakeStore) get(service, key string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	v, ok := f.m[service+"/"+key]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (f *fakeStore) set(service, key, value string) error {
	if f.failSet {
		return errors.New("sin Secret Service")
	}
	f.m[service+"/"+key] = value
	return nil
}

func (f *fakeStore) del(service, key string) error {
	delete(f.m, service+"/"+key)
	return nil
}

// newLock arma un Lock sobre f sin la espera de 1 s; waits cuenta las esperas pedidas.
func newLock(f *fakeStore) (*Lock, *int) {
	l := New(f.get, f.set, f.del)
	waits := 0
	l.sleep = func(time.Duration) { waits++ }
	return l, &waits
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("el error no tiene código: %v", err)
	}
	return e.Code
}

func TestHashRoundTrip(t *testing.T) {
	h1, err := hashPassword("secreto")
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := hashPassword("secreto")
	if !strings.HasPrefix(h1, "$argon2id$v=19$m=65536,t=1,p=4$") {
		t.Fatalf("formato: %s", h1)
	}
	if h1 == h2 {
		t.Fatal("dos hashes de la misma contraseña son iguales")
	}
	if !checkPassword(h1, "secreto") || !checkPassword(h2, "secreto") {
		t.Fatal("la contraseña correcta no verifica")
	}
	if checkPassword(h1, "otra") {
		t.Fatal("una contraseña incorrecta verifica")
	}
	if checkPassword("basura", "secreto") {
		t.Fatal("un hash que no se entiende verifica")
	}
}

func TestUnlockWrongPasswordStaysLocked(t *testing.T) {
	l, waits := newLock(newFakeStore())
	if err := l.SetPassword("", "secreto"); err != nil {
		t.Fatal(err)
	}
	l.LockNow()
	err := l.Unlock("mala")
	if codeOf(t, err) != CodeWrongPassword {
		t.Fatalf("error: %v", err)
	}
	if !l.Locked() {
		t.Fatal("quedó desbloqueado con una contraseña incorrecta")
	}
	if *waits != 1 {
		t.Fatalf("esperas: %d", *waits)
	}
	if err := l.Unlock("secreto"); err != nil || l.Locked() {
		t.Fatalf("no desbloqueó con la correcta: %v", err)
	}
}

func TestStartsLockedWhenEnabled(t *testing.T) {
	f := newFakeStore()
	l, _ := newLock(f)
	if l.Enabled() || l.Locked() {
		t.Fatal("sin hash guardado no debería estar activado ni bloqueado")
	}
	if err := l.SetPassword("", "secreto"); err != nil {
		t.Fatal(err)
	}
	if l.Locked() {
		t.Fatal("activar el bloqueo no debería bloquear")
	}
	l2, _ := newLock(f)
	if !l2.Enabled() || !l2.Locked() {
		t.Fatal("un Lock nuevo con un hash guardado tiene que arrancar bloqueado")
	}
}

func TestSetPasswordRequiresCurrent(t *testing.T) {
	f := newFakeStore()
	l, _ := newLock(f)
	if err := l.SetPassword("", "secreto"); err != nil {
		t.Fatal(err)
	}
	before := f.m["osat/app-lock"]
	if codeOf(t, l.SetPassword("mala", "nueva1")) != CodeWrongPassword {
		t.Fatal("una actual incorrecta tendría que dar error")
	}
	if f.m["osat/app-lock"] != before {
		t.Fatal("cambió el hash guardado")
	}
	if err := l.SetPassword("secreto", "nueva1"); err != nil {
		t.Fatal(err)
	}
	if err := l.Unlock("nueva1"); err != nil {
		t.Fatalf("la contraseña nueva no sirve: %v", err)
	}
}

func TestDisableRequiresCurrent(t *testing.T) {
	f := newFakeStore()
	l, _ := newLock(f)
	if codeOf(t, l.Disable("x")) != CodeNotEnabled {
		t.Fatal("sin bloqueo activo tendría que dar NOT_ENABLED")
	}
	if err := l.SetPassword("", "secreto"); err != nil {
		t.Fatal(err)
	}
	if codeOf(t, l.Disable("mala")) != CodeWrongPassword || !l.Enabled() {
		t.Fatal("se desactivó con una contraseña incorrecta")
	}
	if err := l.Disable("secreto"); err != nil {
		t.Fatal(err)
	}
	if l.Enabled() || l.Locked() || len(f.m) != 0 {
		t.Fatal("no quedó desactivado")
	}
}

func TestPasswordTooShort(t *testing.T) {
	l, _ := newLock(newFakeStore())
	for _, p := range []string{"", "abc"} {
		if codeOf(t, l.SetPassword("", p)) != CodeTooShort {
			t.Fatalf("%q tendría que ser demasiado corta", p)
		}
	}
	if l.Enabled() {
		t.Fatal("se activó con una contraseña corta")
	}
	if err := l.SetPassword("", "abcd"); err != nil {
		t.Fatalf("4 caracteres tendrían que alcanzar: %v", err)
	}
}

func TestStoreUnavailable(t *testing.T) {
	f := newFakeStore()
	f.failSet = true
	l, _ := newLock(f)
	if codeOf(t, l.SetPassword("", "secreto")) != CodeStoreUnavailable {
		t.Fatal("tendría que dar STORE_UNAVAILABLE")
	}
	if l.Enabled() {
		t.Fatal("quedó activado sin poder guardar el hash")
	}
}

func TestLockNowWithoutPasswordDoesNothing(t *testing.T) {
	l, _ := newLock(newFakeStore())
	l.LockNow()
	if l.Locked() {
		t.Fatal("sin contraseña no hay cómo desbloquear")
	}
}

// Si el almacén falla al leer al arrancar, osat arranca bloqueado y vuelve a leer al desbloquear.
func TestStoreReadFailureStartsLocked(t *testing.T) {
	f := newFakeStore()
	l0, _ := newLock(f)
	if err := l0.SetPassword("", "clave1"); err != nil {
		t.Fatal(err)
	}
	f.getErr = errors.New("Secret Service no responde")
	l, _ := newLock(f)
	if !l.Locked() || !l.Enabled() {
		t.Fatal("con el almacén caído tiene que arrancar bloqueado")
	}
	if err := l.Unlock("clave1"); codeOf(t, err) != CodeStoreUnavailable {
		t.Fatalf("con el almacén caído no se puede verificar: %v", err)
	}
	f.getErr = nil
	if err := l.Unlock("mala"); codeOf(t, err) != CodeWrongPassword {
		t.Fatalf("esperaba contraseña incorrecta: %v", err)
	}
	if err := l.Unlock("clave1"); err != nil || l.Locked() {
		t.Fatalf("con el almacén de vuelta tiene que desbloquear: %v", err)
	}
}

// Un sistema sin almacén (Android) arranca sin bloqueo.
func TestUnsupportedPlatformStartsUnlocked(t *testing.T) {
	f := newFakeStore()
	f.getErr = keyring.ErrUnsupportedPlatform
	l, _ := newLock(f)
	if l.Locked() || l.Enabled() {
		t.Fatal("sin almacén no hay bloqueo posible")
	}
}
