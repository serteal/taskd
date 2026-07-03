package secret

import (
	"runtime"
	"testing"
)

func TestOpenUnknownBackend(t *testing.T) {
	if _, err := Open("vault", ""); err == nil {
		t.Fatal("Open(vault): want error, got nil")
	}
}

func TestOpenFileRequiresDir(t *testing.T) {
	if _, err := Open("file", ""); err == nil {
		t.Fatal("Open(file, \"\"): want error, got nil")
	}
}

func TestOpenAuto(t *testing.T) {
	s, err := Open("", t.TempDir())
	if err != nil {
		t.Fatalf("Open(auto): %v", err)
	}
	// Auto picks keychain on darwin, file elsewhere. Constructing the
	// keychain store does not touch the keychain.
	switch runtime.GOOS {
	case "darwin":
		if _, ok := s.(*keychainStore); !ok {
			t.Fatalf("auto on darwin: got %T, want *keychainStore", s)
		}
	default:
		if _, ok := s.(*fileStore); !ok {
			t.Fatalf("auto on %s: got %T, want *fileStore", runtime.GOOS, s)
		}
	}
}

func TestOpenKeychainNonDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("darwin: keychain backend is valid here")
	}
	if _, err := Open("keychain", ""); err == nil {
		t.Fatal("Open(keychain) off darwin: want error, got nil")
	}
}
