package main

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

func newLsCmd(a *app) *cobra.Command {
	var (
		filter    string
		all       bool
		completed bool
		project   string
		label     string
	)
	cmd := &cobra.Command{
		Use:   "ls [VIEW]",
		Short: "List items (active only by default; snoozed items hide until they return)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// Base filter and order: a named view wins, then --completed /
			// --all, then the active-only default.
			var base, orderBy string
			switch {
			case len(args) == 1:
				resp, err := a.client.Views().GetView(ctx, &taskcorev1.GetViewRequest{Name: args[0]})
				if err != nil {
					return fmt.Errorf("view %q: %w (try `task view ls`)", args[0], err)
				}
				base, orderBy = resp.GetView().GetFilter(), resp.GetView().GetOrderBy()
			case completed:
				base = `completed`
			case all:
				base = ""
			default:
				// Active only: has a todo (un-triaged mirrors live in the
				// `inbox` view, not here), not completed, not snoozed.
				base, orderBy = `has(item.todo) && !completed && !snoozed`, "due"
			}

			frags := []string{base}
			if project != "" {
				frags = append(frags, "project == "+celQuote(project))
			}
			if label != "" {
				frags = append(frags, celQuote(label)+" in labels")
			}
			if filter != "" {
				frags = append(frags, filter)
			}

			var items []*taskcorev1.Item
			if _, err := a.client.QueryAll(ctx, andFilter(frags...), orderBy, func(it *taskcorev1.Item) error {
				items = append(items, it)
				return nil
			}); err != nil {
				return err
			}

			if a.json {
				for _, it := range items {
					if err := printProto(cmd.OutOrStdout(), it); err != nil {
						return err
					}
				}
				return nil
			}
			now := time.Now()
			ids := make([]string, len(items))
			for i, it := range items {
				ids[i] = it.GetId()
			}
			prefix := uniquePrefixes(ids)
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tTITLE\tPROJECT\tLABELS\tDUE")
			for _, it := range items {
				title := itemTitle(it)
				if it.GetTodo().GetCompleted() {
					title = "✓ " + title
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					prefix[it.GetId()],
					title,
					it.GetTodo().GetProject(),
					strings.Join(it.GetTodo().GetLabels(), ","),
					humanDue(it.GetTodo().GetDue(), now),
				)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVarP(&filter, "filter", "f", "", "CEL filter, AND-ed with the base")
	cmd.Flags().BoolVar(&all, "all", false, "no filter: everything, including completed")
	cmd.Flags().BoolVar(&completed, "completed", false, "the archive: completed items")
	cmd.Flags().StringVarP(&project, "project", "p", "", "restrict to a project")
	cmd.Flags().StringVarP(&label, "label", "l", "", "restrict to a label")
	return cmd
}
