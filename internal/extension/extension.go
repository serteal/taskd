// Package extension is taskd's extension host. It is deliberately dumb:
// extensions are folders under $TASKD_DIR/extensions, and the daemon's
// entire involvement is (1) supervising each extension's syncer process,
// which talks back through the public API like any other client, and (2)
// serving each extension's web/ folder to the frontend. The daemon never
// learns what an extension means — there is no plugin protocol, no schema
// registration, no capability negotiation.
//
//	~/.taskd/extensions/<name>/
//	  manifest.json   {"name": "<name>", "syncer": ["./binary", "args…"], "web": true}
//	  web/main.js     ESM bundle the frontend loads (when "web" is true)
//	  …               anything else (config, token caches) — never served
package extension

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Manifest struct {
	// Name must equal the folder name: lowercase letters, digits, dashes.
	Name string `json:"name"`
	// Syncer is the argv of the daemon-half process; relative paths resolve
	// against the extension folder. Empty means no daemon half.
	Syncer []string `json:"syncer,omitempty"`
	// Web declares a frontend half in web/ (entry point web/main.js),
	// served at /ext/<name>/. ONLY web/ is served — config and credentials
	// elsewhere in the folder stay private.
	Web bool `json:"web,omitempty"`
}

type Extension struct {
	Manifest
	Dir string
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Scan reads dir's extension folders. A missing dir means no extensions;
// a malformed manifest is an error (a broken install should be loud).
func Scan(dir string) ([]Extension, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var exts []Extension
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		extDir := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(filepath.Join(extDir, "manifest.json"))
		if os.IsNotExist(err) {
			continue // just a folder, not an extension
		}
		if err != nil {
			return nil, err
		}
		var m Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("extension %s: bad manifest: %w", e.Name(), err)
		}
		if m.Name != e.Name() || !nameRE.MatchString(m.Name) {
			return nil, fmt.Errorf("extension %s: manifest name %q must equal the folder name (lowercase letters, digits, dashes)", e.Name(), m.Name)
		}
		exts = append(exts, Extension{Manifest: m, Dir: extDir})
	}
	return exts, nil
}

// Supervise runs the extension's syncer until ctx ends, restarting it with
// backoff on exit. TASKD_ADDR points the process at this daemon; its
// working directory is the extension folder.
func Supervise(ctx context.Context, ext Extension, addr string) {
	if len(ext.Syncer) == 0 {
		return
	}
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := runOnce(ctx, ext, addr)
		if ctx.Err() != nil {
			return
		}
		log.Printf("ext %s: syncer exited: %v (restarting in %s)", ext.Name, err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second // it ran fine for a while; forgive
		} else {
			backoff = min(backoff*2, 30*time.Second)
		}
	}
}

func runOnce(ctx context.Context, ext Extension, addr string) error {
	argv := append([]string(nil), ext.Syncer...)
	if filepath.Base(argv[0]) != argv[0] && !filepath.IsAbs(argv[0]) {
		argv[0] = filepath.Join(ext.Dir, argv[0])
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = ext.Dir
	cmd.Env = append(os.Environ(), "TASKD_ADDR=http://"+addr)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout // one interleaved, prefixed stream
	if err := cmd.Start(); err != nil {
		return err
	}
	go prefixLines(ext.Name, stdout)
	return cmd.Wait()
}

func prefixLines(name string, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for sc.Scan() {
		log.Printf("ext %s: %s", name, sc.Text())
	}
}

// ErrUnknownExtension is returned by Host.SetEnabled for a name that is not
// an installed extension.
var ErrUnknownExtension = errors.New("unknown extension")

// Info is one extension's identity, capabilities, and current enabled state,
// as reported by Host.List for the admin API.
type Info struct {
	Name      string
	HasSyncer bool
	HasWeb    bool
	Enabled   bool
}

// Host owns the runtime side of the extensions: supervising each enabled
// extension's syncer and serving each enabled extension's web bundle. The
// enabled set is adjustable at runtime — disabling an extension stops its
// syncer and stops serving its bundle, enabling reverses both, with no
// daemon restart. A disabled extension stays installed but dormant; the
// disabled set persists in config (see daemon.saveFileConfig).
type Host struct {
	addr string                  // daemon listen address, passed to syncers as TASKD_ADDR
	exts []Extension             // every scanned extension, in scan order
	web  map[string]http.Handler // name -> web/ file server, for extensions with a web half

	mu       sync.Mutex
	ctx      context.Context               // base context for supervision; set by Start
	disabled map[string]bool               // names left dormant (may include not-installed names)
	cancels  map[string]context.CancelFunc // name -> running syncer's canceller
}

// NewHost builds a Host over the scanned extensions. addr is the daemon's
// listen address (handed to syncers as TASKD_ADDR); disabled is the set of
// extension names to keep dormant. Nothing is supervised until Start.
func NewHost(exts []Extension, addr string, disabled map[string]bool) *Host {
	h := &Host{
		addr:     addr,
		exts:     exts,
		web:      make(map[string]http.Handler),
		disabled: make(map[string]bool, len(disabled)),
		cancels:  make(map[string]context.CancelFunc),
	}
	for name := range disabled {
		h.disabled[name] = true
	}
	for _, ext := range exts {
		if ext.Web {
			// Only web/ is served; config and credentials elsewhere stay private.
			h.web[ext.Name] = http.FileServerFS(os.DirFS(filepath.Join(ext.Dir, "web")))
		}
	}
	return h
}

// Start begins supervising every enabled extension's syncer, deriving each
// from ctx so canceling ctx stops them all. It also records ctx so newly
// enabled extensions can be supervised later via SetEnabled.
func (h *Host) Start(ctx context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ctx = ctx
	for _, ext := range h.exts {
		if h.disabled[ext.Name] {
			continue
		}
		h.startLocked(ext)
		if ext.Web {
			log.Printf("taskd: extension %s: serving web bundle at /ext/%s/", ext.Name, ext.Name)
		}
	}
}

// SetEnabled turns one extension on or off in the running daemon, applying
// the change immediately: enabling starts its syncer and (re)serves its
// bundle, disabling stops the syncer and stops serving. It is a no-op if the
// extension is already in the requested state. An unknown name is
// ErrUnknownExtension.
func (h *Host) SetEnabled(name string, enable bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ext, ok := h.find(name)
	if !ok {
		return ErrUnknownExtension
	}
	if enable == !h.disabled[name] {
		return nil // already in the requested state
	}
	if enable {
		delete(h.disabled, name)
		h.startLocked(ext)
	} else {
		h.disabled[name] = true
		h.stopLocked(name)
	}
	return nil
}

// List reports every installed extension with its capabilities and current
// enabled state, in scan order.
func (h *Host) List() []Info {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Info, 0, len(h.exts))
	for _, ext := range h.exts {
		out = append(out, Info{
			Name:      ext.Name,
			HasSyncer: len(ext.Syncer) > 0,
			HasWeb:    ext.Web,
			Enabled:   !h.disabled[ext.Name],
		})
	}
	return out
}

// Disabled returns the current disabled set, sorted, for persistence. It
// includes any names that were disabled in config but are not installed, so
// round-tripping the config never silently drops them.
func (h *Host) Disabled() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.disabled))
	for name := range h.disabled {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// startLocked supervises ext's syncer under a fresh child of the base
// context, if it has a syncer and isn't already running. Caller holds mu.
func (h *Host) startLocked(ext Extension) {
	if len(ext.Syncer) == 0 || h.ctx == nil {
		return
	}
	if _, running := h.cancels[ext.Name]; running {
		return
	}
	ctx, cancel := context.WithCancel(h.ctx)
	h.cancels[ext.Name] = cancel
	go Supervise(ctx, ext, h.addr)
	log.Printf("taskd: extension %s: supervising syncer", ext.Name)
}

// stopLocked cancels ext's supervision, killing its syncer process. Caller
// holds mu.
func (h *Host) stopLocked(name string) {
	if cancel, ok := h.cancels[name]; ok {
		cancel()
		delete(h.cancels, name)
		log.Printf("taskd: extension %s: stopped syncer", name)
	}
}

func (h *Host) find(name string) (Extension, bool) {
	for _, ext := range h.exts {
		if ext.Name == name {
			return ext, true
		}
	}
	return Extension{}, false
}

// Handler serves /ext/index.json (the enabled extensions that have a web
// half) and /ext/<name>/* from each enabled extension's web/ folder only.
// Disabled extensions are absent from the index and 404 on their prefix.
func (h *Host) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extensions iterate fast; never let a browser cache a stale bundle.
		w.Header().Set("Cache-Control", "no-cache")
		if r.URL.Path == "/ext/index.json" {
			h.serveIndex(w)
			return
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/ext/"), "/")
		h.mu.Lock()
		srv, served := h.web[name]
		enabled := !h.disabled[name]
		h.mu.Unlock()
		if !served || !enabled {
			http.NotFound(w, r)
			return
		}
		http.StripPrefix("/ext/"+name+"/", srv).ServeHTTP(w, r)
	})
}

func (h *Host) serveIndex(w http.ResponseWriter) {
	type entry struct {
		Name string `json:"name"`
	}
	index := []entry{}
	h.mu.Lock()
	for _, ext := range h.exts {
		if ext.Web && !h.disabled[ext.Name] {
			index = append(index, entry{Name: ext.Name})
		}
	}
	h.mu.Unlock()
	b, _ := json.Marshal(index)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}
