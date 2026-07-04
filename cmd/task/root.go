package main

import (
	"io"

	"github.com/spf13/cobra"

	"todoapp/gen/task/taskconnect"
	"todoapp/pkg/client"
)

// app carries what every subcommand needs: the daemon address, the output
// writer (injected so tests capture it), and a lazily built client.
type app struct {
	addr string
	json bool
	out  io.Writer

	tc taskconnect.TaskServiceClient
}

func (a *app) client() taskconnect.TaskServiceClient {
	if a.tc == nil {
		a.tc = client.New(a.addr)
	}
	return a.tc
}

func newRootCmd(out io.Writer) *cobra.Command {
	a := &app{out: out}
	root := &cobra.Command{
		Use:           "task",
		Short:         "task manages todos in a taskd daemon",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(out)
	root.PersistentFlags().StringVar(&a.addr, "addr", client.Target(), "taskd address (http://host:port or unix:///path)")
	root.PersistentFlags().BoolVar(&a.json, "json", false, "print protojson instead of human-readable output")

	root.AddCommand(
		newAddCmd(a),
		newLsCmd(a),
		newShowCmd(a),
		newDoneCmd(a),
		newUndoneCmd(a),
		newEditCmd(a),
		newRmCmd(a),
		newLabelsCmd(a),
		newWatchCmd(a),
		newImportCmd(a),
		newExportCmd(a),
	)
	return root
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
