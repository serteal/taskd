package main

import (
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskpb "todoapp/gen/task"
)

func newAddCmd(a *app) *cobra.Command {
	var (
		labels []string
		notes  string
		due    string
	)
	cmd := &cobra.Command{
		Use:   "add TITLE...",
		Short: "Create a task",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &taskpb.CreateTaskRequest{
				Title:  strings.Join(args, " "),
				Notes:  notes,
				Labels: labels,
			}
			if due != "" {
				t, err := parseWhen(due, time.Now())
				if err != nil {
					return err
				}
				req.DueTime = timestamppb.New(t)
			}
			res, err := a.client().CreateTask(cmd.Context(), connect.NewRequest(req))
			if err != nil {
				return err
			}
			created := res.Msg.GetTask()
			fmt.Fprintf(a.out, "%s %s\n", shortID(created.GetId()), created.GetTitle())
			return nil
		},
	}
	cmd.Flags().StringArrayVarP(&labels, "label", "l", nil, "label to attach (repeatable)")
	cmd.Flags().StringVar(&notes, "notes", "", "free-form notes")
	cmd.Flags().StringVar(&due, "due", "", "due time (today, tomorrow, Nd, YYYY-MM-DD[ HH:MM])")
	return cmd
}
