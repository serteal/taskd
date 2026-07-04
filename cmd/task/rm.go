package main

import (
	"bufio"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	taskpb "github.com/serteal/taskd/gen/task"
)

func newRmCmd(a *app) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "rm ID...",
		Short: "Delete tasks",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			in := bufio.NewReader(cmd.InOrStdin())
			for _, ref := range args {
				t, err := resolveTask(ctx, a.client(), ref)
				if err != nil {
					return err
				}
				if !force {
					fmt.Fprintf(a.out, "delete %s? [y/N] ", t.GetTitle())
					line, _ := in.ReadString('\n') // EOF reads as "no"
					switch strings.ToLower(strings.TrimSpace(line)) {
					case "y", "yes":
					default:
						fmt.Fprintf(a.out, "skipped %s\n", shortID(t.GetId()))
						continue
					}
				}
				if _, err := a.client().DeleteTask(ctx, connect.NewRequest(&taskpb.DeleteTaskRequest{Id: t.GetId()})); err != nil {
					return err
				}
				fmt.Fprintf(a.out, "deleted %s %s\n", shortID(t.GetId()), t.GetTitle())
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "delete without confirmation")
	return cmd
}
