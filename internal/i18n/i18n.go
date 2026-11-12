// Package i18n traduce los textos que arma Go y llegan a la persona (errores, avisos,
// diálogos nativos, páginas del navegador integrado). El log sigue en español.
package i18n

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

const (
	ES = "es"
	EN = "en"
)

type entry struct{ es, en string }

// catalog: clave → texto en cada idioma. Los verbos % tienen que ser los mismos en los dos.
var catalog = map[string]entry{
	// Ejemplo de la base de i18n; los textos reales se agregan en las tareas siguientes.
	"example.hello": {es: "Hola, %s", en: "Hello, %s"},
}

var (
	mu   sync.RWMutex
	lang = ES
)

// SetLanguage cambia el idioma. "es" y "en" se usan tal cual; cualquier otro valor
// (por ejemplo "system") se resuelve con el idioma del sistema.
func SetLanguage(l string) {
	l = strings.ToLower(strings.TrimSpace(l))
	if l != ES && l != EN {
		l = systemLanguage()
	}
	mu.Lock()
	lang = l
	mu.Unlock()
}

// Language devuelve el idioma en uso ("es" o "en").
func Language() string {
	mu.RLock()
	defer mu.RUnlock()
	return lang
}

// systemLanguage: español si el idioma del entorno (LC_ALL, LC_MESSAGES, LANG) lo es; si no, inglés.
// Sin ninguna de esas variables (Windows) queda español: el frontend lo corrige al cargar con navigator.language.
func systemLanguage() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			if strings.HasPrefix(strings.ToLower(v), "es") {
				return ES
			}
			return EN
		}
	}
	return ES
}

// T devuelve el texto de la clave en el idioma en uso, con args aplicados (fmt.Sprintf).
// Una clave inexistente devuelve la clave.
func T(key string, args ...any) string {
	e, ok := catalog[key]
	if !ok {
		return key
	}
	s := e.es
	if Language() == EN && e.en != "" {
		s = e.en
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// msgError es un error cuyo texto se traduce al pedirlo (Error), no al crearlo: sirve para los
// errores de paquete (var errX = i18n.Err(...)) y sigue el idioma que haya en ese momento.
type msgError struct {
	key     string
	args    []any
	wrapped error
}

func (e *msgError) Error() string { return T(e.key, e.args...) }
func (e *msgError) Unwrap() error { return e.wrapped }

// Err crea un error con el texto de la clave. El primer argumento que sea un error queda envuelto
// (errors.Is y errors.As lo ven); en el texto se escribe con %v.
func Err(key string, args ...any) error {
	e := &msgError{key: key, args: args}
	for _, a := range args {
		if err, ok := a.(error); ok {
			e.wrapped = err
			break
		}
	}
	return e
}
