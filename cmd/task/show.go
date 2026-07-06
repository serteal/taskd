package main

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskpb "github.com/serteal/taskd/gen/task"
	"github.com/serteal/taskd/gen/task/taskconnect"
	"github.com/serteal/taskd/internal/recur"
)

func newShowCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show ID",
		Short: "Show a task in full",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			t, err := resolveTask(ctx, a.client(), args[0])
			if err != nil {
				return err
			}
			if a.json {
				b, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(t)
				if err != nil {
					return err
				}
				fmt.Fprintf(a.out, "%s\n", b)
				return nil
			}

			w := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
			fmt.Fprintf(w, "id:\t%s\n", t.GetId())
			fmt.Fprintf(w, "title:\t%s\n", t.GetTitle())
			if t.GetNotes() != "" {
				fmt.Fprintf(w, "notes:\t%s\n", t.GetNotes())
			}
			if len(t.GetLabels()) > 0 {
				fmt.Fprintf(w, "labels:\t%s\n", strings.Join(t.GetLabels(), ", "))
			}
			if dt := t.GetDueTime(); dt != nil {
				fmt.Fprintf(w, "due:\t%s\n", localStamp(dt))
			}
			if ct := t.GetCompletedTime(); ct != nil {
				fmt.Fprintf(w, "completed:\t%s\n", localStamp(ct))
			}
			if rule := t.GetRecurrence(); rule != "" {
				fmt.Fprintf(w, "recurrence:\t%s (%s)\n", rule, recur.Humanize(rule))
			}
			if pid := t.GetParentId(); pid != "" {
				parent, err := a.client().GetTask(ctx, connect.NewRequest(&taskpb.GetTaskRequest{Id: pid}))
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "parent:\t%s %s\n", shortID(pid), parent.Msg.GetTask().GetTitle())
			}
			if t.GetSource() != "" {
				fmt.Fprintf(w, "source:\t%s\n", t.GetSource())
				fmt.Fprintf(w, "external ref:\t%s\n", t.GetExternalRef())
			}
			fmt.Fprintf(w, "revision:\t%d\n", t.GetRevision())
			fmt.Fprintf(w, "created:\t%s\n", localStamp(t.GetCreateTime()))
			fmt.Fprintf(w, "updated:\t%s\n", localStamp(t.GetUpdateTime()))
			if err := w.Flush(); err != nil {
				return err
			}
			if ed := t.GetExternalData(); ed != nil {
				b, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(ed)
				if err != nil {
					return err
				}
				fmt.Fprintf(a.out, "external data:\n%s\n", b)
			}
			children, err := childrenOf(ctx, a.client(), t.GetId())
			if err != nil {
				return err
			}
			if len(children) > 0 {
				fmt.Fprintf(a.out, "subtasks:\n")
				for _, c := range children {
					mark := " "
					if c.GetCompletedTime() != nil {
						mark = "✓"
					}
					fmt.Fprintf(a.out, "  %s %s %s\n", mark, shortID(c.GetId()), c.GetTitle())
				}
			}
			return nil
		},
	}
}

// childrenOf lists a task's subtasks (both completion states). There is no
// server-side parent filter, so it pages the full list and keeps the matches.
func childrenOf(ctx context.Context, tc taskconnect.TaskServiceClient, parentID string) ([]*taskpb.Task, error) {
	all, err := listAll(ctx, tc, &taskpb.TaskFilter{}, "created asc", 0)
	if err != nil {
		return nil, err
	}
	var children []*taskpb.Task
	for _, t := range all {
		if t.GetParentId() == parentID {
			children = append(children, t)
		}
	}
	return children, nil
}

func localStamp(ts *timestamppb.Timestamp) string {
	return ts.AsTime().Local().Format("2006-01-02 15:04:05")
}
