package store

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// La clave de las credenciales de los sitios (credvault.go) se guarda protegida con DPAPI: solo la puede abrir
// la misma cuenta de Windows en esta PC.

var keyEntropy = []byte("osat-site-credentials")

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func takeBlob(out windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...)
}

func protectKey(key []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptProtectData(blob(key), nil, blob(keyEntropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return takeBlob(out), nil
}

func unprotectKey(b []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(blob(b), nil, blob(keyEntropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return takeBlob(out), nil
}
