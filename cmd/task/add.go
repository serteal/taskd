package main

import (
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskpb "github.com/serteal/taskd/gen/task"
	"github.com/serteal/taskd/internal/recur"
)

func newAddCmd(a *app) *cobra.Command {
	var (
		labels []string
		notes  string
		due    string
		every  string
		parent string
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
			if every != "" {
				rule, err := recur.FromNatural(every)
				if err != nil {
					return err
				}
				req.Recurrence = rule
			}
			if parent != "" {
				p, err := resolveTask(cmd.Context(), a.client(), parent)
				if err != nil {
					return err
				}
				req.ParentId = p.GetId()
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
	cmd.Flags().StringVar(&due, "due", "", `due time (e.g. today, tomorrow, friday, 3d, 2w, "in 2 weeks", 2026-12-24, "fri 3pm")`)
	cmd.Flags().StringVar(&every, "every", "", `recurrence in plain English (e.g. daily, weekday, "2 weeks", "mon,wed,fri")`)
	cmd.Flags().StringVar(&parent, "parent", "", "parent task (id or unique id prefix) to nest this task under")
	return cmd
}
