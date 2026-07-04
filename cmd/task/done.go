package main

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskpb "todoapp/gen/task"
)

func newDoneCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "done ID...",
		Short: "Mark tasks completed",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setCompleted(cmd.Context(), a, args, true)
		},
	}
}

func newUndoneCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "undone ID...",
		Short: "Re-open completed tasks",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setCompleted(cmd.Context(), a, args, false)
		},
	}
}

// setCompleted masks completed_time only: setting it completes the task,
// leaving it unset under the mask re-opens it.
func setCompleted(ctx context.Context, a *app, refs []string, done bool) error {
	for _, ref := range refs {
		t, err := resolveTask(ctx, a.client(), ref)
		if err != nil {
			return err
		}
		upd := &taskpb.Task{}
		if done {
			upd.CompletedTime = timestamppb.Now()
		}
		res, err := a.client().UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
			Id:         t.GetId(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"completed_time"}},
			Task:       upd,
		}))
		if err != nil {
			return err
		}
		got := res.Msg.GetTask()
		mark := "reopened"
		if done {
			mark = "done"
		}
		fmt.Fprintf(a.out, "%s %s %s\n", mark, shortID(got.GetId()), got.GetTitle())
	}
	return nil
}
