// Command task is the CLI for taskd. It talks to the daemon exclusively
// through the public TaskService API (pkg/client + gen); it has no access to
// the store or server internals.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd(os.Stdout).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
