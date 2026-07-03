package secret

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// keychainService is the -s (service) attribute for every taskd item; the
// account attribute carries the caller-namespaced key.
const keychainService = "taskd"

const securityBin = "/usr/bin/security"

// errSecItemNotFound is `security`'s exit status when no matching item
// exists.
const errSecItemNotFound = 44

// runner executes /usr/bin/security with args and returns its stdout.
// Injectable so unit tests can assert command construction without ever
// touching the real keychain.
type runner func(args ...string) ([]byte, error)

// keychainStore shells out to /usr/bin/security. Values are stored as
// base64 text: `security` mangles binary passwords, text survives.
type keychainStore struct {
	run runner
}

func newKeychainStore() *keychainStore {
	return &keychainStore{run: runSecurity}
}

func (s *keychainStore) Put(key string, value []byte) error {
	// -U updates in place when the item already exists.
	_, err := s.run("add-generic-password", "-U",
		"-s", keychainService, "-a", key,
		"-w", base64.StdEncoding.EncodeToString(value))
	if err != nil {
		return fmt.Errorf("secret: keychain put %q: %w", key, err)
	}
	return nil
}

func (s *keychainStore) Get(key string) ([]byte, bool, error) {
	out, err := s.run("find-generic-password",
		"-s", keychainService, "-a", key, "-w")
	if err != nil {
		if isNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("secret: keychain get %q: %w", key, err)
	}
	v, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		return nil, false, fmt.Errorf("secret: keychain get %q: corrupt value: %w", key, err)
	}
	return v, true, nil
}

func (s *keychainStore) Delete(key string) error {
	_, err := s.run("delete-generic-password",
		"-s", keychainService, "-a", key)
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("secret: keychain delete %q: %w", key, err)
	}
	return nil // idempotent
}

// securityError is a non-zero exit from /usr/bin/security.
type securityError struct {
	code   int
	stderr string
}

func (e *securityError) Error() string {
	return fmt.Sprintf("security exited %d: %s", e.code, e.stderr)
}

func isNotFound(err error) bool {
	var se *securityError
	if !errors.As(err, &se) {
		return false
	}
	return se.code == errSecItemNotFound ||
		strings.Contains(se.stderr, "could not be found")
}

func runSecurity(args ...string) ([]byte, error) {
	cmd := exec.Command(securityBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return stdout.Bytes(), &securityError{code: code, stderr: strings.TrimSpace(stderr.String())}
	}
	return stdout.Bytes(), nil
}
