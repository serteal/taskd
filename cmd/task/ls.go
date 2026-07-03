package main

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	taskcorev1 "todoapp/gen/taskcore/v1"
	viewv1 "todoapp/gen/taskcore/view/v1"
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

			// Server-side rendering: the core evaluates contribution
			// expressions and effective dues (override-wins through facet
			// bindings) — this CLI never could for plugin kinds.
			var items []*taskcorev1.Item
			var rows []*viewv1.RenderedRow
			token := ""
			for {
				resp, err := a.client.Items().QueryItems(ctx, &taskcorev1.QueryItemsRequest{
					Filter: andFilter(frags...), OrderBy: orderBy,
					PageSize: 200, PageToken: token,
					Render: &viewv1.RenderSpec{Columns: []string{"id", "title", "project", "labels", "due"}},
				})
				if err != nil {
					return err
				}
				items = append(items, resp.GetItems()...)
				rows = append(rows, resp.GetRows()...)
				token = resp.GetNextPageToken()
				if token == "" {
					break
				}
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
			for i, row := range rows {
				title := row.GetCells()[1].GetText()
				if items[i].GetTodo().GetCompleted() {
					title = "✓ " + title
				}
				for _, b := range row.GetBadges() {
					title += " [" + b.GetLabel() + "]"
				}
				due := ""
				if dt := row.GetCells()[4].GetDatetime(); dt != nil {
					due = humanDue(dt, now)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					prefix[row.GetItemId()],
					title,
					row.GetCells()[2].GetText(),
					row.GetCells()[3].GetText(),
					due,
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
