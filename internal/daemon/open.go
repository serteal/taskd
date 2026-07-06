package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// openURL launches the default browser at url. It is a package variable so
// tests stub the exec; failure is the caller's to log, never fatal.
var openURL = browserOpen

// browserOpen runs the platform's URL opener: open on darwin, xdg-open on
// linux. Other platforms report that no opener is known.
func browserOpen(url string) error {
	var name string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "linux":
		name = "xdg-open"
	default:
		return fmt.Errorf("no browser opener for %s", runtime.GOOS)
	}
	return exec.Command(name, url).Start()
}

// firstRun reports whether dir has no tasks.db yet — the daemon's first start
// in this data directory.
func firstRun(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "tasks.db"))
	return os.IsNotExist(err)
}

// wantOpen decides whether to launch the browser using a strict precedence
// ladder, most specific first: the -open flag forces open; the -no-open flag
// forces closed (and so beats config open: true); the config open: setting
// decides when set; otherwise a first run opens and a later run does not.
func wantOpen(first, openFlag, noOpenFlag bool, cfgOpen *bool) bool {
	switch {
	case openFlag:
		return true
	case noOpenFlag:
		return false
	case cfgOpen != nil:
		return *cfgOpen
	default:
		return first
	}
}
