// task-plugin-ics: read-only ICS calendar connector. Point an instance at
// any .ics URL or file (Google's private ICS link, a team calendar, a local
// file) and its events mirror into the tracker.
package main

import (
	"log/slog"
	"os"

	"todoapp/pkg/taskplugin"
	"todoapp/plugins/ics"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	conn := &ics.Connector{Log: log}
	if err := taskplugin.Serve(ics.Manifest(), conn); err != nil {
		log.Error("plugin exited", "err", err)
		os.Exit(1)
	}
}
