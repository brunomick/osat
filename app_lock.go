package main

// Bloqueo con contraseña (internal/applock). El estado bloqueado vive en Go: el frontend
// lo consulta con LockStatus, así que recargarlo no saltea el bloqueo.

import (
	"log/slog"
	"sync"

	"osat/internal/applock"
	"osat/internal/i18n"
)

// helloAvailable consulta Windows Hello una sola vez: la consulta puede tardar segundos.
var helloAvailable = sync.OnceValue(applock.HelloAvailable)

// LockState es lo que el frontend necesita saber del bloqueo.
type LockState struct {
	Enabled        bool `json:"enabled"`        // hay una contraseña definida
	Locked         bool `json:"locked"`         // osat está bloqueado
	HelloAvailable bool `json:"helloAvailable"` // la PC tiene Windows Hello configurado
	HelloEnabled   bool `json:"helloEnabled"`   // Hello disponible y permitido en Ajustes
}

// LockStatus devuelve el estado del bloqueo.
func (a *App) LockStatus() LockState {
	hello := helloAvailable()
	return LockState{
		Enabled: a.lock.Enabled(), Locked: a.lock.Locked(),
		HelloAvailable: hello, HelloEnabled: hello && a.lockUseHello(),
	}
}

// lockUseHello dice si los ajustes permiten desbloquear con Windows Hello.
func (a *App) lockUseHello() bool {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	return a.settings.General.LockUseHello
}

// LockSetPassword define la contraseña; con el bloqueo activo hay que dar la actual.
func (a *App) LockSetPassword(current, next string) error { return a.lock.SetPassword(current, next) }

// LockDisable apaga el bloqueo; hay que dar la contraseña actual.
func (a *App) LockDisable(current string) error { return a.lock.Disable(current) }

// LockUnlock desbloquea osat si la contraseña es correcta.
func (a *App) LockUnlock(password string) error { return a.lock.Unlock(password) }

// LockUnlockHello desbloquea osat si la persona se verifica con Windows Hello. Si cancela, si
// Hello falla o no está disponible, o si los ajustes no lo permiten, no hace nada y no da error:
// el frontend se queda en la contraseña.
func (a *App) LockUnlockHello() error {
	if !a.lock.Locked() || !a.lockUseHello() {
		return nil
	}
	ok, err := applock.HelloVerify(0, i18n.T("lock.helloMessage"))
	if err != nil {
		slog.Debug("bloqueo: Windows Hello falló", "err", err)
		return nil
	}
	if ok {
		a.lock.UnlockVerified()
	}
	return nil
}

// LockNow bloquea osat (lo usa el bloqueo por inactividad).
func (a *App) LockNow() { a.lock.LockNow() }

// LockWindowReady dice si la ventana de osat está al frente y no minimizada: el frontend espera a
// eso para lanzar Windows Hello, así el diálogo no aparece minimizado ni encima de otra aplicación.
func (a *App) LockWindowReady() bool { return applock.WindowReady() }
