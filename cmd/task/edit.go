package main

import (
	"fmt"
	"slices"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

func newEditCmd(a *app) *cobra.Command {
	var (
		title       string
		note        string
		project     string
		due         string
		clearDue    bool
		snooze      string
		clearSnooze bool
		addLabels   []string
		rmLabels    []string
	)
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit todo fields; the field mask is built from the flags you set",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if due != "" && clearDue {
				return fmt.Errorf("--due and --clear-due are mutually exclusive")
			}
			if snooze != "" && clearSnooze {
				return fmt.Errorf("--snooze and --clear-snooze are mutually exclusive")
			}

			var paths []string
			todo := &taskcorev1.Todo{}
			now := time.Now()
			if cmd.Flags().Changed("title") {
				paths = append(paths, "todo.title_override")
				todo.TitleOverride = title
			}
			if cmd.Flags().Changed("note") {
				paths = append(paths, "todo.note")
				todo.Note = note
			}
			if cmd.Flags().Changed("project") {
				paths = append(paths, "todo.project")
				todo.Project = project
			}
			if due != "" {
				t, err := parseWhen(due, now)
				if err != nil {
					return fmt.Errorf("--due: %w", err)
				}
				paths = append(paths, "todo.due")
				todo.Due = timestamppb.New(t)
			}
			if clearDue {
				paths = append(paths, "todo.due") // nil value clears
			}
			if snooze != "" {
				t, err := parseWhen(snooze, now)
				if err != nil {
					return fmt.Errorf("--snooze: %w", err)
				}
				paths = append(paths, "todo.snoozed_until")
				todo.SnoozedUntil = timestamppb.New(t)
			}
			if clearSnooze {
				paths = append(paths, "todo.snoozed_until")
			}

			labelEdit := len(addLabels) > 0 || len(rmLabels) > 0
			if !labelEdit && len(paths) == 0 {
				return fmt.Errorf("nothing to edit: set at least one flag (see `task edit --help`)")
			}

			ctx := cmd.Context()
			items := a.client.Items()

			if !labelEdit {
				resp, err := items.UpdateItem(ctx, &taskcorev1.UpdateItemRequest{
					Id:         args[0],
					UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
					Item:       &taskcorev1.Item{Todo: todo},
				})
				if err != nil {
					return err
				}
				return editDone(a, cmd, resp.GetItem())
			}

			// Label edits are read-modify-write on the full label list,
			// guarded by expected_todo_revision; one retry on a concurrent
			// write (Aborted).
			paths = append(paths, "todo.labels")
			for attempt := 0; ; attempt++ {
				cur, err := items.GetItem(ctx, &taskcorev1.GetItemRequest{Id: args[0]})
				if err != nil {
					return err
				}
				it := cur.GetItem()
				todo.Labels = editLabels(it.GetTodo().GetLabels(), addLabels, rmLabels)
				resp, err := items.UpdateItem(ctx, &taskcorev1.UpdateItemRequest{
					Id:                   it.GetId(),
					UpdateMask:           &fieldmaskpb.FieldMask{Paths: paths},
					Item:                 &taskcorev1.Item{Todo: todo},
					ExpectedTodoRevision: it.GetTodoRevision(),
				})
				if status.Code(err) == codes.Aborted && attempt == 0 {
					continue // concurrent write: re-read and retry once
				}
				if err != nil {
					return err
				}
				return editDone(a, cmd, resp.GetItem())
			}
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&note, "note", "", "new note")
	cmd.Flags().StringVarP(&project, "project", "p", "", "new project")
	cmd.Flags().StringVar(&due, "due", "", "due WHEN")
	cmd.Flags().BoolVar(&clearDue, "clear-due", false, "remove the due date")
	cmd.Flags().StringVar(&snooze, "snooze", "", "hide until WHEN")
	cmd.Flags().BoolVar(&clearSnooze, "clear-snooze", false, "remove the snooze")
	cmd.Flags().StringArrayVar(&addLabels, "add-label", nil, "add a label (repeatable)")
	cmd.Flags().StringArrayVar(&rmLabels, "rm-label", nil, "remove a label (repeatable)")
	return cmd
}

// editLabels applies add/remove to the current labels, deduplicating while
// preserving order.
func editLabels(current, add, remove []string) []string {
	out := []string{}
	for _, l := range current {
		if !slices.Contains(remove, l) && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	for _, l := range add {
		if !slices.Contains(remove, l) && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	return out
}

func editDone(a *app, cmd *cobra.Command, it *taskcorev1.Item) error {
	if a.json {
		return printProto(cmd.OutOrStdout(), it)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "edited %s %s\n", shortID(it.GetId()), itemTitle(it))
	return nil
}
