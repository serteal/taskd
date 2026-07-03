package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

func newDoneCmd(a *app) *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "done <id>...",
		Short: "Complete items",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setCompleted(a, cmd, args, true, reason)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", `free-form completion reason ("wontdo", "gone", ...)`)
	return cmd
}

func newReopenCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "reopen <id>...",
		Short: "Reopen completed items",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setCompleted(a, cmd, args, false, "")
		},
	}
}

func setCompleted(a *app, cmd *cobra.Command, ids []string, completed bool, reason string) error {
	verb := "completed"
	if !completed {
		verb = "reopened"
	}
	paths := []string{"todo.completed"}
	if reason != "" {
		paths = append(paths, "todo.completed_reason")
	}
	for _, id := range ids {
		resp, err := a.client.Items().UpdateItem(cmd.Context(), &taskcorev1.UpdateItemRequest{
			Id:         id,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
			Item: &taskcorev1.Item{Todo: &taskcorev1.Todo{
				Completed:       completed,
				CompletedReason: reason,
			}},
		})
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		if a.json {
			if err := printProto(cmd.OutOrStdout(), resp.GetItem()); err != nil {
				return err
			}
			continue
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s\n", verb, shortID(resp.GetItem().GetId()), itemTitle(resp.GetItem()))
	}
	return nil
}
