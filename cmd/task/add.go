package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

func newAddCmd(a *app) *cobra.Command {
	var (
		project string
		labels  []string
		due     string
		snooze  string
		note    string
	)
	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Create a native task",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			todo := &taskcorev1.Todo{
				TitleOverride: strings.Join(args, " "),
				Project:       project,
				Labels:        labels,
				Note:          note,
			}
			now := time.Now()
			if due != "" {
				t, err := parseWhen(due, now)
				if err != nil {
					return fmt.Errorf("--due: %w", err)
				}
				todo.Due = timestamppb.New(t)
			}
			if snooze != "" {
				t, err := parseWhen(snooze, now)
				if err != nil {
					return fmt.Errorf("--snooze: %w", err)
				}
				todo.SnoozedUntil = timestamppb.New(t)
			}
			resp, err := a.client.Items().CreateItem(cmd.Context(), &taskcorev1.CreateItemRequest{Todo: todo})
			if err != nil {
				return err
			}
			if a.json {
				return printProto(cmd.OutOrStdout(), resp.GetItem())
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added %s %s\n", shortID(resp.GetItem().GetId()), itemTitle(resp.GetItem()))
			return nil
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "project path, e.g. work/reviews")
	cmd.Flags().StringArrayVarP(&labels, "label", "l", nil, "label (repeatable)")
	cmd.Flags().StringVar(&due, "due", "", "due WHEN (RFC3339, YYYY-MM-DD, today, tomorrow, 3d, 12h)")
	cmd.Flags().StringVar(&snooze, "snooze", "", "hide until WHEN")
	cmd.Flags().StringVar(&note, "note", "", "free-form note")
	return cmd
}
