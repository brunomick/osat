// Package applock es el bloqueo de osat con contraseña. Es una pantalla de bloqueo: no cifra
// las credenciales, que siguen en el almacén del sistema. El estado bloqueado vive acá, en Go,
// para que recargar el frontend no lo saltee. La contraseña se guarda solo como hash argon2id.
package applock

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/argon2"
)

const (
	service = "osat"
	key     = "app-lock"

	// Parámetros de argon2id del hash que se guarda.
	argonMemory  = 65536 // KiB
	argonTime    = 1
	argonThreads = 4
	argonKeyLen  = 32
	saltLen      = 16

	// MinPasswordLen es el largo mínimo (en caracteres) de la contraseña.
	MinPasswordLen = 4

	wrongPasswordDelay = time.Second
)

// Lock guarda el hash de la contraseña en el almacén del sistema y si osat está bloqueado.
type Lock struct {
	get func(service, key string) (string, error)
	set func(service, key, value string) error
	del func(service, key string) error

	// sleep espera tras una contraseña incorrecta; los tests lo reemplazan.
	sleep func(time.Duration)

	mu     sync.Mutex
	hash   string // vacío = bloqueo no activado
	locked bool
	// unread: el almacén falló al leer el hash al arrancar. Se arranca bloqueado (no se sabe si
	// hay contraseña) y se vuelve a leer al usarlo.
	unread bool
}

// New recibe las funciones del almacén (en osat, las de go-keyring). Si ya hay un hash
// guardado, arranca bloqueado. Si el almacén falla al leerlo, también arranca bloqueado:
// osat no abre sin contraseña. Solo un sistema sin almacén (Android) arranca sin bloqueo.
func New(get func(service, key string) (string, error),
	set func(service, key, value string) error,
	del func(service, key string) error) *Lock {
	l := &Lock{get: get, set: set, del: del, sleep: time.Sleep}
	h, err := get(service, key)
	switch {
	case err == nil && h != "":
		l.hash = h
		l.locked = true
	case err != nil && !errors.Is(err, keyring.ErrNotFound) && !errors.Is(err, keyring.ErrUnsupportedPlatform):
		l.unread = true
		l.locked = true
	}
	return l
}

// loadLocked vuelve a leer el hash si al arrancar no se pudo (l.mu tomado).
func (l *Lock) loadLocked() error {
	if !l.unread {
		return nil
	}
	h, err := l.get(service, key)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return newErr(CodeStoreUnavailable, "lock.storeUnavailable", err)
	}
	l.hash, l.unread = h, false
	if h == "" {
		l.locked = false
	}
	return nil
}

// Enabled dice si hay una contraseña definida.
func (l *Lock) Enabled() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.hash != "" || l.unread
}

// Locked dice si osat está bloqueado.
func (l *Lock) Locked() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.locked
}

// SetPassword define la contraseña. Con el bloqueo activo hay que dar la actual.
func (l *Lock) SetPassword(current, next string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return err
	}
	if l.hash != "" {
		if err := l.verifyLocked(current); err != nil {
			return err
		}
	}
	if utf8.RuneCountInString(next) < MinPasswordLen {
		return newErr(CodeTooShort, "lock.tooShort", MinPasswordLen)
	}
	h, err := hashPassword(next)
	if err != nil {
		return newErr(CodeStoreUnavailable, "lock.storeUnavailable", err)
	}
	if err := l.set(service, key, h); err != nil {
		return newErr(CodeStoreUnavailable, "lock.storeUnavailable", err)
	}
	l.hash = h
	return nil
}

// Disable apaga el bloqueo; hay que dar la contraseña actual.
func (l *Lock) Disable(current string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return err
	}
	if l.hash == "" {
		return newErr(CodeNotEnabled, "lock.notEnabled")
	}
	if err := l.verifyLocked(current); err != nil {
		return err
	}
	if err := l.del(service, key); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return newErr(CodeStoreUnavailable, "lock.storeUnavailable", err)
	}
	l.hash = ""
	l.locked = false
	return nil
}

// Unlock desbloquea si la contraseña es correcta.
func (l *Lock) Unlock(password string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return err
	}
	if l.hash == "" {
		return newErr(CodeNotEnabled, "lock.notEnabled")
	}
	if err := l.verifyLocked(password); err != nil {
		return err
	}
	l.locked = false
	return nil
}

// UnlockVerified desbloquea sin contraseña: quien llama ya verificó a la persona (Windows Hello).
func (l *Lock) UnlockVerified() {
	l.mu.Lock()
	l.locked = false
	l.mu.Unlock()
}

// LockNow bloquea osat. Sin contraseña definida no hace nada: no habría cómo desbloquear.
func (l *Lock) LockNow() {
	l.mu.Lock()
	l.locked = l.hash != "" || l.unread
	l.mu.Unlock()
}

// verifyLocked compara password con el hash guardado (l.mu tomado). Si no coincide espera 1 s
// antes de responder, para frenar los intentos en serie.
func (l *Lock) verifyLocked(password string) error {
	if checkPassword(l.hash, password) {
		return nil
	}
	l.sleep(wrongPasswordDelay)
	return newErr(CodeWrongPassword, "lock.wrongPassword")
}

// hashPassword devuelve el hash en formato PHC: $argon2id$v=19$m=65536,t=1,p=4$<sal b64>$<hash b64>.
func hashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(sum)), nil
}

// checkPassword compara en tiempo constante. Un hash que no se entiende nunca coincide.
func checkPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || t == 0 || p == 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
