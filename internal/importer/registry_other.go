//go:build !windows

package importer

// readRegistry: fuera de Windows no hay registro; las sesiones se importan
// desde un archivo exportado.
func readRegistry(string) (map[string]map[string]string, error) {
	return nil, ErrNotFound
}
