package store

import "github.com/zalando/go-keyring"

// Dónde van los secretos: el keyring del sistema. En Android no hay uno al que llegar desde Go y
// secrets_android.go los cambia por un archivo cifrado.
var (
	secretSet    = keyring.Set
	secretGet    = keyring.Get
	secretDelete = keyring.Delete
)
