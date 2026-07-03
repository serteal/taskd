// Package secret is the core secret store (DESIGN.md §10, §14): plugins own
// their auth flows, the core stores opaque blobs so the easy path is the safe
// one and no plugin invents plaintext token files. The core never parses what
// it stores. On darwin the default backend is the OS keychain; elsewhere a
// 0600 JSON file.
package secret

import (
	"fmt"
	"runtime"
)

// Store holds opaque plugin secrets. Namespacing is the caller's job.
type Store interface {
	Put(key string, value []byte) error
	Get(key string) ([]byte, bool, error) // found=false when absent
	Delete(key string) error              // idempotent
}

// Open picks a backend: "keychain" (darwin only), "file", or "" = auto
// (darwin→keychain, else file). dir is where the file backend keeps
// secrets.json; the keychain backend ignores it.
func Open(backend, dir string) (Store, error) {
	if backend == "" {
		if runtime.GOOS == "darwin" {
			backend = "keychain"
		} else {
			backend = "file"
		}
	}
	switch backend {
	case "keychain":
		if runtime.GOOS != "darwin" {
			return nil, fmt.Errorf("secret: keychain backend is darwin-only (GOOS=%s)", runtime.GOOS)
		}
		return newKeychainStore(), nil
	case "file":
		return newFileStore(dir)
	default:
		return nil, fmt.Errorf("secret: unknown backend %q", backend)
	}
}
