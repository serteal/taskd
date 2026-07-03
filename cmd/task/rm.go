package main

import (
	"fmt"

	"github.com/spf13/cobra"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

func newRmCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id>",
		Short: "Delete a native task (tracked items are completed, never deleted)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The server refuses non-native items; its message explains why.
			if _, err := a.client.Items().DeleteItem(cmd.Context(), &taskcorev1.DeleteItemRequest{Id: args[0]}); err != nil {
				return err
			}
			if !a.json {
				fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", shortID(args[0]))
			}
			return nil
		},
	}
}
