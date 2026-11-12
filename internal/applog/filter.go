package applog

import (
	"context"
	"log/slog"
	"strings"
)

// redacted reemplaza el valor de los atributos con clave sensible.
const redacted = "[oculto]"

// sensitiveKeys son los fragmentos que vuelven sensible a una clave (sin distinguir mayúsculas).
var sensitiveKeys = []string{"secret", "password", "pass", "passphrase", "token"}

func sensitive(key string) bool {
	k := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// redact oculta el valor de a si su clave es sensible; en un grupo revisa cada atributo.
func redact(a slog.Attr) slog.Attr {
	if sensitive(a.Key) {
		return slog.String(a.Key, redacted)
	}
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		attrs := v.Group()
		out := make([]slog.Attr, len(attrs))
		for i, g := range attrs {
			out[i] = redact(g)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	}
	return a
}

// secretHandler envuelve a otro handler y oculta los atributos sensibles.
// Es una red de seguridad: la regla es no pasar nunca secretos al log.
type secretHandler struct {
	inner slog.Handler
}

func (h secretHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h secretHandler) Handle(ctx context.Context, r slog.Record) error {
	nr := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		nr.AddAttrs(redact(a))
		return true
	})
	return h.inner.Handle(ctx, nr)
}

func (h secretHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = redact(a)
	}
	return secretHandler{h.inner.WithAttrs(out)}
}

func (h secretHandler) WithGroup(name string) slog.Handler {
	return secretHandler{h.inner.WithGroup(name)}
}
