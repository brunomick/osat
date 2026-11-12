//go:build windows

package importer

import (
	"errors"
	"strconv"

	"golang.org/x/sys/windows/registry"
)

// readRegistry lee de HKCU\<key> los valores de texto y DWORD de cada
// subclave (una por sesión), por nombre de subclave.
func readRegistry(key string) (map[string]map[string]string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.ENUMERATE_SUB_KEYS)
	if errors.Is(err, registry.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer k.Close()
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, ErrNotFound
	}
	out := map[string]map[string]string{}
	for _, name := range names {
		sk, err := registry.OpenKey(k, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		vals := map[string]string{}
		vnames, _ := sk.ReadValueNames(-1)
		for _, vn := range vnames {
			if s, _, err := sk.GetStringValue(vn); err == nil {
				vals[vn] = s
			} else if n, _, err := sk.GetIntegerValue(vn); err == nil {
				vals[vn] = strconv.FormatUint(n, 10)
			}
		}
		sk.Close()
		out[name] = vals
	}
	return out, nil
}
