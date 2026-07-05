package adminserver_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"connectrpc.com/connect"

	adminpb "github.com/serteal/taskd/gen/admin"
	"github.com/serteal/taskd/gen/admin/adminconnect"
	"github.com/serteal/taskd/internal/adminserver"
	"github.com/serteal/taskd/internal/extension"
)

// persistLog records the disabled sets handed to the persist hook, so tests
// can assert what the server would have written to config.
type persistLog struct {
	mu   sync.Mutex
	last []string
	n    int
}

func (p *persistLog) save(disabled []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.last = append([]string(nil), disabled...)
	p.n++
	return nil
}

func (p *persistLog) snapshot() ([]string, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.last...), p.n
}

// newTestStack builds a Host over two web-only extensions (alpha enabled,
// beta initially disabled) and serves both the AdminService and the /ext/
// handler on one HTTP server — the same wiring the daemon uses.
func newTestStack(t *testing.T) (adminconnect.AdminServiceClient, *persistLog, string) {
	t.Helper()
	root := t.TempDir()
	exts := []extension.Extension{
		mkExt(t, root, "alpha"),
		mkExt(t, root, "beta"),
	}
	host := extension.NewHost(exts, "127.0.0.1:0", map[string]bool{"beta": true})
	host.Start(context.Background())

	pl := &persistLog{}
	mux := http.NewServeMux()
	path, handler := adminserver.New(host, pl.save).Handler()
	mux.Handle(path, handler)
	mux.Handle("/ext/", host.Handler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return adminconnect.NewAdminServiceClient(ts.Client(), ts.URL), pl, ts.URL
}

// mkExt creates a web-only extension folder with a served bundle.
func mkExt(t *testing.T, root, name string) extension.Extension {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "web", "main.js"), []byte("export default {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return extension.Extension{Manifest: extension.Manifest{Name: name, Web: true}, Dir: dir}
}

func enabledMap(res *adminpb.ListExtensionsResponse) map[string]bool {
	out := map[string]bool{}
	for _, e := range res.GetExtensions() {
		out[e.GetName()] = e.GetEnabled()
	}
	return out
}

func TestListReflectsDisabled(t *testing.T) {
	ac, _, base := newTestStack(t)
	ctx := context.Background()

	res, err := ac.ListExtensions(ctx, connect.NewRequest(&adminpb.ListExtensionsRequest{}))
	if err != nil {
		t.Fatalf("ListExtensions: %v", err)
	}
	if len(res.Msg.GetExtensions()) != 2 {
		t.Fatalf("got %d extensions, want 2", len(res.Msg.GetExtensions()))
	}
	en := enabledMap(res.Msg)
	if !en["alpha"] || en["beta"] {
		t.Fatalf("enabled state = %v, want alpha=true beta=false", en)
	}
	// Capabilities are reported.
	for _, e := range res.Msg.GetExtensions() {
		if !e.GetHasWeb() || e.GetHasSyncer() {
			t.Errorf("%s: hasWeb=%v hasSyncer=%v, want web-only", e.GetName(), e.GetHasWeb(), e.GetHasSyncer())
		}
	}

	// Disabled beta is absent from the index and 404s on its prefix.
	if idx := httpGet(t, base+"/ext/index.json"); idx != `[{"name":"alpha"}]` {
		t.Errorf("/ext/index.json = %s, want only alpha", idx)
	}
	if code := statusOf(t, base+"/ext/beta/main.js"); code != http.StatusNotFound {
		t.Errorf("disabled beta bundle served (status %d), want 404", code)
	}
	if code := statusOf(t, base+"/ext/alpha/main.js"); code != http.StatusOK {
		t.Errorf("enabled alpha bundle status %d, want 200", code)
	}
}

func TestSetExtensionEnabledFlipsAndPersists(t *testing.T) {
	ac, pl, base := newTestStack(t)
	ctx := context.Background()

	// Enable beta: hot-applied, persisted with an empty disabled set.
	res, err := ac.SetExtensionEnabled(ctx, connect.NewRequest(&adminpb.SetExtensionEnabledRequest{Name: "beta", Enabled: true}))
	if err != nil {
		t.Fatalf("enable beta: %v", err)
	}
	if res.Msg.GetRestartRequired() {
		t.Errorf("restart_required = true, want hot-apply")
	}
	if got, n := pl.snapshot(); n != 1 || len(got) != 0 {
		t.Errorf("persisted disabled = %v (calls=%d), want [] after 1 call", got, n)
	}
	// Beta is now listed and served.
	if code := statusOf(t, base+"/ext/beta/main.js"); code != http.StatusOK {
		t.Errorf("beta bundle status %d after enable, want 200", code)
	}
	if idx := httpGet(t, base+"/ext/index.json"); idx != `[{"name":"alpha"},{"name":"beta"}]` {
		t.Errorf("/ext/index.json = %s, want alpha+beta", idx)
	}

	// Disable alpha: persisted disabled set becomes [alpha], bundle 404s.
	if _, err := ac.SetExtensionEnabled(ctx, connect.NewRequest(&adminpb.SetExtensionEnabledRequest{Name: "alpha", Enabled: false})); err != nil {
		t.Fatalf("disable alpha: %v", err)
	}
	if got, n := pl.snapshot(); n != 2 || len(got) != 1 || got[0] != "alpha" {
		t.Errorf("persisted disabled = %v (calls=%d), want [alpha]", got, n)
	}
	if code := statusOf(t, base+"/ext/alpha/main.js"); code != http.StatusNotFound {
		t.Errorf("alpha bundle status %d after disable, want 404", code)
	}

	// List now reflects the flipped state.
	list, err := ac.ListExtensions(ctx, connect.NewRequest(&adminpb.ListExtensionsRequest{}))
	if err != nil {
		t.Fatalf("ListExtensions: %v", err)
	}
	if en := enabledMap(list.Msg); en["alpha"] || !en["beta"] {
		t.Fatalf("enabled state = %v, want alpha=false beta=true", en)
	}
}

func TestSetExtensionEnabledErrors(t *testing.T) {
	ac, pl, _ := newTestStack(t)
	ctx := context.Background()

	// Unknown extension → NOT_FOUND, nothing persisted.
	_, err := ac.SetExtensionEnabled(ctx, connect.NewRequest(&adminpb.SetExtensionEnabledRequest{Name: "nope", Enabled: false}))
	wantCode(t, err, connect.CodeNotFound)

	// Empty name → INVALID_ARGUMENT.
	_, err = ac.SetExtensionEnabled(ctx, connect.NewRequest(&adminpb.SetExtensionEnabledRequest{Name: "", Enabled: true}))
	wantCode(t, err, connect.CodeInvalidArgument)

	if _, n := pl.snapshot(); n != 0 {
		t.Errorf("persist called %d times on error, want 0", n)
	}
}

func wantCode(t *testing.T, err error, code connect.Code) {
	t.Helper()
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != code {
		t.Fatalf("got error %v, want code %v", err, code)
	}
}

func httpGet(t *testing.T, url string) string {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d", url, res.StatusCode)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func statusOf(t *testing.T, url string) int {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	res.Body.Close()
	return res.StatusCode
}
