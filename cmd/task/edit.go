package main

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskpb "todoapp/gen/task"
)

func newEditCmd(a *app) *cobra.Command {
	var (
		title, notes, due       string
		clearDue                bool
		addLabels, removeLabels []string
	)
	cmd := &cobra.Command{
		Use:   "edit ID",
		Short: "Edit fields of a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cur, err := resolveTask(ctx, a.client(), args[0])
			if err != nil {
				return err
			}

			// One masked write carrying only the touched paths, guarded by
			// the revision we just read so a concurrent writer can't be
			// silently overwritten.
			upd := &taskpb.Task{}
			var paths []string
			if cmd.Flags().Changed("title") {
				upd.Title = title
				paths = append(paths, "title")
			}
			if cmd.Flags().Changed("notes") {
				upd.Notes = notes
				paths = append(paths, "notes")
			}
			switch {
			case clearDue:
				paths = append(paths, "due_time") // masked but unset clears
			case cmd.Flags().Changed("due"):
				t, err := parseWhen(due, time.Now())
				if err != nil {
					return err
				}
				upd.DueTime = timestamppb.New(t)
				paths = append(paths, "due_time")
			}
			if len(addLabels)+len(removeLabels) > 0 {
				upd.Labels = editLabels(cur.GetLabels(), addLabels, removeLabels)
				paths = append(paths, "labels")
			}
			if len(paths) == 0 {
				return errors.New("nothing to edit: pass at least one of --title, --notes, --due, --clear-due, --add-label, --remove-label")
			}

			res, err := a.client().UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
				Id:               cur.GetId(),
				UpdateMask:       &fieldmaskpb.FieldMask{Paths: paths},
				Task:             upd,
				ExpectedRevision: cur.GetRevision(),
			}))
			if err != nil {
				if connect.CodeOf(err) == connect.CodeAborted {
					return errors.New("task changed underneath you; re-run")
				}
				return err
			}
			got := res.Msg.GetTask()
			fmt.Fprintf(a.out, "%s %s\n", shortID(got.GetId()), got.GetTitle())
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&notes, "notes", "", "new notes (empty clears)")
	cmd.Flags().StringVar(&due, "due", "", "new due time (today, tomorrow, Nd, YYYY-MM-DD[ HH:MM])")
	cmd.Flags().BoolVar(&clearDue, "clear-due", false, "remove the due date")
	cmd.Flags().StringArrayVar(&addLabels, "add-label", nil, "label to add (repeatable)")
	cmd.Flags().StringArrayVar(&removeLabels, "remove-label", nil, "label to remove (repeatable)")
	cmd.MarkFlagsMutuallyExclusive("due", "clear-due")
	return cmd
}

func editLabels(cur, add, remove []string) []string {
	set := make(map[string]bool, len(cur)+len(add))
	for _, l := range cur {
		set[l] = true
	}
	for _, l := range add {
		set[l] = true
	}
	for _, l := range remove {
		delete(set, l)
	}
	out := make([]string, 0, len(set))
	for l := range set {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}
