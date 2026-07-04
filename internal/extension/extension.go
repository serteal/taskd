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
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// Handler serves /ext/index.json (which extensions have a web half) and
// /ext/<name>/* from each extension's web/ folder only.
func Handler(exts []Extension) http.Handler {
	type entry struct {
		Name string `json:"name"`
	}
	index := []entry{}
	mux := http.NewServeMux()
	for _, ext := range exts {
		if !ext.Web {
			continue
		}
		index = append(index, entry{Name: ext.Name})
		prefix := "/ext/" + ext.Name + "/"
		mux.Handle(prefix, http.StripPrefix(prefix,
			http.FileServerFS(os.DirFS(filepath.Join(ext.Dir, "web")))))
	}
	indexJSON, _ := json.Marshal(index)
	mux.HandleFunc("/ext/index.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(indexJSON)
	})
	// Extensions iterate fast; never let a browser cache a stale bundle.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		mux.ServeHTTP(w, r)
	})
}
