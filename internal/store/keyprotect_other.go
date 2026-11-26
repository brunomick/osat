//go:build !windows

package store

// Fuera de Windows no hay DPAPI: la clave de las credenciales de los sitios (credvault.go) va tal cual en
// credentials.key, que solo puede leer la persona (0600). Lo que importa es que no esté en el almacén del sistema,
// junto a los datos cifrados.

func protectKey(key []byte) ([]byte, error) { return append([]byte(nil), key...), nil }

func unprotectKey(b []byte) ([]byte, error) { return append([]byte(nil), b...), nil }
