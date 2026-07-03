package secret

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"testing"
	"time"
)

// fakeRunner records every invocation and replays a canned reply, so the
// keychain backend's command construction is testable without ever touching
// the real keychain.
type fakeRunner struct {
	calls [][]string
	out   []byte
	err   error
}

func (f *fakeRunner) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	return f.out, f.err
}

func TestKeychainPutCommand(t *testing.T) {
	f := &fakeRunner{}
	s := &keychainStore{run: f.run}
	val := []byte{0x00, 0xff, 'a', '\n'}
	if err := s.Put("cal@a/token", val); err != nil {
		t.Fatalf("Put: %v", err)
	}
	want := []string{
		"add-generic-password", "-U",
		"-s", "taskd", "-a", "cal@a/token",
		"-w", base64.StdEncoding.EncodeToString(val),
	}
	if len(f.calls) != 1 || !reflect.DeepEqual(f.calls[0], want) {
		t.Fatalf("Put args = %q, want %q", f.calls, want)
	}
}

func TestKeychainGetCommand(t *testing.T) {
	val := []byte("s3cr3t")
	f := &fakeRunner{out: []byte(base64.StdEncoding.EncodeToString(val) + "\n")}
	s := &keychainStore{run: f.run}
	got, found, err := s.Get("cal@a/token")
	if err != nil || !found || !bytes.Equal(got, val) {
		t.Fatalf("Get: %q found=%v err=%v", got, found, err)
	}
	want := []string{"find-generic-password", "-s", "taskd", "-a", "cal@a/token", "-w"}
	if len(f.calls) != 1 || !reflect.DeepEqual(f.calls[0], want) {
		t.Fatalf("Get args = %q, want %q", f.calls, want)
	}
}

func TestKeychainGetAbsent(t *testing.T) {
	// security exits 44 (errSecItemNotFound) when the item does not exist.
	f := &fakeRunner{err: &securityError{code: 44, stderr: "The specified item could not be found in the keychain."}}
	s := &keychainStore{run: f.run}
	got, found, err := s.Get("nope")
	if err != nil || found || got != nil {
		t.Fatalf("Get absent: %q found=%v err=%v, want absent", got, found, err)
	}
}

func TestKeychainGetOtherError(t *testing.T) {
	f := &fakeRunner{err: &securityError{code: 1, stderr: "keychain locked"}}
	s := &keychainStore{run: f.run}
	if _, _, err := s.Get("k"); err == nil {
		t.Fatal("Get with real failure: want error, got nil")
	}
}

func TestKeychainDeleteCommand(t *testing.T) {
	f := &fakeRunner{}
	s := &keychainStore{run: f.run}
	if err := s.Delete("cal@a/token"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	want := []string{"delete-generic-password", "-s", "taskd", "-a", "cal@a/token"}
	if len(f.calls) != 1 || !reflect.DeepEqual(f.calls[0], want) {
		t.Fatalf("Delete args = %q, want %q", f.calls, want)
	}
}

func TestKeychainDeleteIdempotent(t *testing.T) {
	f := &fakeRunner{err: &securityError{code: 44, stderr: "The specified item could not be found in the keychain."}}
	s := &keychainStore{run: f.run}
	if err := s.Delete("nope"); err != nil {
		t.Fatalf("Delete absent: %v, want nil", err)
	}
	f2 := &fakeRunner{err: &securityError{code: 1, stderr: "keychain locked"}}
	s2 := &keychainStore{run: f2.run}
	if err := s2.Delete("k"); err == nil {
		t.Fatal("Delete with real failure: want error, got nil")
	}
}

// TestKeychainReal exercises the actual macOS keychain. It is opt-in: unit
// runs must never touch the real keychain.
func TestKeychainReal(t *testing.T) {
	if os.Getenv("TASKD_KEYCHAIN_TEST") == "" {
		t.Skip("set TASKD_KEYCHAIN_TEST=1 to run against the real keychain")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("keychain backend is darwin-only")
	}
	s, err := Open("keychain", "")
	if err != nil {
		t.Fatalf("Open(keychain): %v", err)
	}
	key := fmt.Sprintf("taskd-test/%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = s.Delete(key) })

	val := []byte{0x00, 0x01, 0xfe, 0xff}
	if err := s.Put(key, val); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, found, err := s.Get(key)
	if err != nil || !found || !bytes.Equal(got, val) {
		t.Fatalf("Get: %x found=%v err=%v", got, found, err)
	}
	if err := s.Delete(key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, err := s.Get(key); err != nil || found {
		t.Fatalf("Get after delete: found=%v err=%v", found, err)
	}
	if err := s.Delete(key); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
}
