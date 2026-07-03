package secret

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestFileStore(t *testing.T) (Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open("file", dir)
	if err != nil {
		t.Fatalf("Open(file): %v", err)
	}
	return s, dir
}

func TestFileRoundTrip(t *testing.T) {
	s, _ := newTestFileStore(t)
	// Binary-hostile value: every byte, including NUL and newline.
	val := make([]byte, 256)
	for i := range val {
		val[i] = byte(i)
	}
	if err := s.Put("cal@personal/oauth", val); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, found, err := s.Get("cal@personal/oauth")
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v", found, err)
	}
	if !bytes.Equal(got, val) {
		t.Fatalf("Get: got %x, want %x", got, val)
	}
}

func TestFileAbsent(t *testing.T) {
	s, _ := newTestFileStore(t)
	got, found, err := s.Get("nope")
	if err != nil {
		t.Fatalf("Get absent: %v", err)
	}
	if found || got != nil {
		t.Fatalf("Get absent: found=%v value=%q, want absent", found, got)
	}
}

func TestFileDeleteIdempotent(t *testing.T) {
	s, _ := newTestFileStore(t)
	if err := s.Delete("never-existed"); err != nil {
		t.Fatalf("Delete absent: %v", err)
	}
	if err := s.Put("k", []byte("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, err := s.Get("k"); err != nil || found {
		t.Fatalf("Get after delete: found=%v err=%v", found, err)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
}

func TestFilePermissions(t *testing.T) {
	s, dir := newTestFileStore(t)
	if err := s.Put("k", []byte("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, "secrets.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("secrets.json mode = %o, want 0600", perm)
	}
}

func TestFileAtomicOverwrite(t *testing.T) {
	s, dir := newTestFileStore(t)
	if err := s.Put("k", []byte("old")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put("k", []byte("new")); err != nil {
		t.Fatalf("overwrite Put: %v", err)
	}
	got, found, err := s.Get("k")
	if err != nil || !found || string(got) != "new" {
		t.Fatalf("Get after overwrite: %q found=%v err=%v", got, found, err)
	}
	// No temp droppings left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file %s", e.Name())
		}
	}
	// A fresh store over the same dir sees the persisted value.
	s2, err := Open("file", dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, found, err = s2.Get("k")
	if err != nil || !found || string(got) != "new" {
		t.Fatalf("Get after reopen: %q found=%v err=%v", got, found, err)
	}
}

func TestFileWeirdKeys(t *testing.T) {
	s, _ := newTestFileStore(t)
	keys := []string{
		"cal@personal/oauth token",
		"a/b/c",
		"spaces and / slashes",
		`quotes " and \ backslashes`,
		"unicode/κλειδί",
	}
	for i, k := range keys {
		want := []byte(fmt.Sprintf("value-%d", i))
		if err := s.Put(k, want); err != nil {
			t.Fatalf("Put(%q): %v", k, err)
		}
		got, found, err := s.Get(k)
		if err != nil || !found || !bytes.Equal(got, want) {
			t.Fatalf("Get(%q): %q found=%v err=%v", k, got, found, err)
		}
	}
}

func TestFileConcurrent(t *testing.T) {
	s, _ := newTestFileStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := fmt.Sprintf("k%d", i)
			if err := s.Put(k, []byte(k)); err != nil {
				t.Errorf("Put(%s): %v", k, err)
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < 16; i++ {
		k := fmt.Sprintf("k%d", i)
		got, found, err := s.Get(k)
		if err != nil || !found || string(got) != k {
			t.Fatalf("Get(%s): %q found=%v err=%v", k, got, found, err)
		}
	}
}
