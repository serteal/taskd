package main

import (
	"fmt"
	"text/tabwriter"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	taskpb "todoapp/gen/task"
)

func newLabelsCmd(a *app) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "labels",
		Short: "List labels in use with task counts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := a.client().ListLabels(cmd.Context(), connect.NewRequest(&taskpb.ListLabelsRequest{
				IncludeCompleted: all,
			}))
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
			for _, lc := range res.Msg.GetLabels() {
				fmt.Fprintf(w, "%s\t%d\n", lc.GetLabel(), lc.GetCount())
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "count completed tasks too")
	return cmd
}
