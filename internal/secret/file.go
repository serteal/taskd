package secret

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// fileStore keeps secrets in <dir>/secrets.json, mode 0600, values base64.
// Every write rewrites the whole file atomically (temp file + rename), so a
// crash mid-write never corrupts the store. The mutex makes it safe within
// one process; the daemon is the only writer by design.
type fileStore struct {
	mu   sync.Mutex
	path string
}

func newFileStore(dir string) (*fileStore, error) {
	if dir == "" {
		return nil, errors.New("secret: file backend requires a directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secret: create dir: %w", err)
	}
	return &fileStore{path: filepath.Join(dir, "secrets.json")}, nil
}

func (s *fileStore) Put(key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	m[key] = base64.StdEncoding.EncodeToString(value)
	return s.save(m)
}

func (s *fileStore) Get(key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return nil, false, err
	}
	enc, ok := m[key]
	if !ok {
		return nil, false, nil
	}
	v, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, false, fmt.Errorf("secret: corrupt value for %q: %w", key, err)
	}
	return v, true, nil
}

func (s *fileStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	if _, ok := m[key]; !ok {
		return nil // idempotent
	}
	delete(m, key)
	return s.save(m)
}

func (s *fileStore) load() (map[string]string, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("secret: read: %w", err)
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("secret: parse %s: %w", s.path, err)
	}
	return m, nil
}

func (s *fileStore) save(m map[string]string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("secret: encode: %w", err)
	}
	dir := filepath.Dir(s.path)
	f, err := os.CreateTemp(dir, ".secrets-*.tmp") // CreateTemp opens 0600
	if err != nil {
		return fmt.Errorf("secret: temp file: %w", err)
	}
	tmp := f.Name()
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("secret: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("secret: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("secret: close: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("secret: rename: %w", err)
	}
	return nil
}
