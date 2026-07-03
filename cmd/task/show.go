package main

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

func newShowCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one item in full (unique id prefixes work)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := a.client.Items().GetItem(cmd.Context(), &taskcorev1.GetItemRequest{Id: args[0]})
			if err != nil {
				return err
			}
			it := resp.GetItem()
			if a.json {
				return printProto(cmd.OutOrStdout(), it)
			}

			now := time.Now()
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			row := func(k, v string) { fmt.Fprintf(w, "%s\t%s\n", k, v) }
			row("title:", itemTitle(it))
			row("id:", it.GetId())
			row("kind:", it.GetKind())
			todo := it.GetTodo()
			if p := todo.GetProject(); p != "" {
				row("project:", p)
			}
			if ls := todo.GetLabels(); len(ls) > 0 {
				row("labels:", strings.Join(ls, ", "))
			}
			if d := todo.GetDue(); d != nil {
				row("due:", absRel(d, now))
			}
			if s := todo.GetSnoozedUntil(); s != nil {
				row("snoozed until:", absRel(s, now))
			}
			if n := todo.GetNote(); n != "" {
				row("note:", n)
			}
			if todo.GetCompleted() {
				v := "yes"
				if at := todo.GetCompletedAt(); at != nil {
					v += " (" + at.AsTime().Local().Format("2006-01-02 15:04") + ")"
				}
				if r := todo.GetCompletedReason(); r != "" {
					v += " reason: " + r
				}
				row("completed:", v)
			} else {
				row("completed:", "no")
			}
			if m := it.GetMirror(); m != nil {
				row("remote:", m.GetLink().GetExternalUrl())
				row("remote state:", m.GetState())
			}
			row("created:", it.GetCreatedAt().AsTime().Local().Format("2006-01-02 15:04"))
			row("updated:", it.GetUpdatedAt().AsTime().Local().Format("2006-01-02 15:04"))
			row("revisions:", fmt.Sprintf("todo %d, mirror %d", it.GetTodoRevision(), it.GetMirrorRevision()))
			return w.Flush()
		},
	}
	return cmd
}

// absRel formats a timestamp as absolute local time plus the humanized
// day-distance, e.g. "2026-07-04 23:59 (1d)".
func absRel(ts *timestamppb.Timestamp, now time.Time) string {
	abs := ts.AsTime().Local().Format("2006-01-02 15:04")
	if rel := humanDue(ts, now); rel != "" {
		return abs + " (" + rel + ")"
	}
	return abs
}
