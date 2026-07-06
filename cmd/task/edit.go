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

	taskpb "github.com/serteal/taskd/gen/task"
	"github.com/serteal/taskd/internal/recur"
)

func newEditCmd(a *app) *cobra.Command {
	var (
		title, notes, due       string
		clearDue                bool
		addLabels, removeLabels []string
		every                   string
		clearEvery              bool
		parent                  string
		clearParent             bool
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
			switch {
			case clearEvery:
				paths = append(paths, "recurrence") // masked but unset stops recurring
			case cmd.Flags().Changed("every"):
				rule, err := recur.FromNatural(every)
				if err != nil {
					return err
				}
				upd.Recurrence = rule
				paths = append(paths, "recurrence")
			}
			switch {
			case clearParent:
				paths = append(paths, "parent_id") // masked but unset detaches to top-level
			case cmd.Flags().Changed("parent"):
				p, err := resolveTask(ctx, a.client(), parent)
				if err != nil {
					return err
				}
				upd.ParentId = p.GetId()
				paths = append(paths, "parent_id")
			}
			if len(paths) == 0 {
				return errors.New("nothing to edit: pass at least one of --title, --notes, --due, --clear-due, --add-label, --remove-label, --every, --clear-every, --parent, --clear-parent")
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
	cmd.Flags().StringVar(&due, "due", "", `new due time (e.g. today, tomorrow, friday, 3d, 2w, "in 2 weeks", 2026-12-24, "fri 3pm")`)
	cmd.Flags().BoolVar(&clearDue, "clear-due", false, "remove the due date")
	cmd.Flags().StringArrayVar(&addLabels, "add-label", nil, "label to add (repeatable)")
	cmd.Flags().StringArrayVar(&removeLabels, "remove-label", nil, "label to remove (repeatable)")
	cmd.Flags().StringVar(&every, "every", "", `set recurrence in plain English (e.g. daily, weekday, "2 weeks")`)
	cmd.Flags().BoolVar(&clearEvery, "clear-every", false, "stop the task recurring")
	cmd.Flags().StringVar(&parent, "parent", "", "nest under this parent task (id or unique id prefix)")
	cmd.Flags().BoolVar(&clearParent, "clear-parent", false, "detach from its parent (back to top-level)")
	cmd.MarkFlagsMutuallyExclusive("due", "clear-due")
	cmd.MarkFlagsMutuallyExclusive("every", "clear-every")
	cmd.MarkFlagsMutuallyExclusive("parent", "clear-parent")
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
