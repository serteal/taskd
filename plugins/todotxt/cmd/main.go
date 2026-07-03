// task-plugin-todotxt: a bidirectional connector for a single todo.txt file
// (http://todotxt.org). Point an instance at an absolute path and its task
// lines mirror into the tracker; local completion and edits flow back as
// intents that rewrite the file in place.
package main

import (
	"log/slog"
	"os"

	"todoapp/pkg/taskplugin"
	"todoapp/plugins/todotxt"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	conn := &todotxt.Connector{Log: log}
	if err := taskplugin.Serve(todotxt.Manifest(), conn); err != nil {
		log.Error("plugin exited", "err", err)
		os.Exit(1)
	}
}
