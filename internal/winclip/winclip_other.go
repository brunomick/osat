//go:build !windows

package winclip

// Fuera de Windows osat no lee archivos del portapapeles del sistema.

func Current() Info { return Info{} }

func Paths() ([]string, error) { return nil, ErrNothing }

func SaveVirtual(dir string) ([]string, error) { return nil, ErrNothing }
