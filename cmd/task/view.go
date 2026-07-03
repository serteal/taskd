package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

func newViewCmd(a *app) *cobra.Command {
	view := &cobra.Command{
		Use:   "view",
		Short: "Manage saved views (named CEL filters)",
	}

	var (
		filter      string
		orderBy     string
		description string
	)
	save := &cobra.Command{
		Use:   "save <name>",
		Short: "Create or update a saved view",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := a.client.Views().SaveView(cmd.Context(), &taskcorev1.SaveViewRequest{
				View: &taskcorev1.View{
					Name:        args[0],
					Filter:      filter,
					OrderBy:     orderBy,
					Description: description,
				},
			})
			if err != nil {
				return err
			}
			if a.json {
				return printProto(cmd.OutOrStdout(), resp.GetView())
			}
			fmt.Fprintf(cmd.OutOrStdout(), "saved view %s\n", resp.GetView().GetName())
			return nil
		},
	}
	save.Flags().StringVarP(&filter, "filter", "f", "", "CEL filter (required)")
	save.Flags().StringVar(&orderBy, "order-by", "", "sort key: due, created_at, updated_at, project, kind (+ ' desc')")
	save.Flags().StringVar(&description, "description", "", "what this view is for")
	_ = save.MarkFlagRequired("filter")

	ls := &cobra.Command{
		Use:   "ls",
		Short: "List saved views",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := a.client.Views().ListViews(cmd.Context(), &taskcorev1.ListViewsRequest{})
			if err != nil {
				return err
			}
			if a.json {
				for _, v := range resp.GetViews() {
					if err := printProto(cmd.OutOrStdout(), v); err != nil {
						return err
					}
				}
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tFILTER\tORDER\tDESCRIPTION")
			for _, v := range resp.GetViews() {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", v.GetName(), v.GetFilter(), v.GetOrderBy(), v.GetDescription())
			}
			return w.Flush()
		},
	}

	rm := &cobra.Command{
		Use:   "rm <name>",
		Short: "Delete a saved view",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := a.client.Views().DeleteView(cmd.Context(), &taskcorev1.DeleteViewRequest{Name: args[0]}); err != nil {
				return err
			}
			if !a.json {
				fmt.Fprintf(cmd.OutOrStdout(), "deleted view %s\n", args[0])
			}
			return nil
		},
	}

	view.AddCommand(save, ls, rm)
	return view
}
