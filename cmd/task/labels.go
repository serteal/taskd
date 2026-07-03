package main

import (
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

func newLabelsCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "labels",
		Short: "Distinct labels with item counts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printCounts(a, cmd, func(it *taskcorev1.Item) []string {
				return it.GetTodo().GetLabels()
			})
		},
	}
}

func newProjectsCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "projects",
		Short: "Distinct projects with item counts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printCounts(a, cmd, func(it *taskcorev1.Item) []string {
				if p := it.GetTodo().GetProject(); p != "" {
					return []string{p}
				}
				return nil
			})
		},
	}
}

// printCounts tallies the values fn yields per item, over ALL items
// (client-side; the archive counts too).
func printCounts(a *app, cmd *cobra.Command, fn func(*taskcorev1.Item) []string) error {
	counts := map[string]int{}
	if _, err := a.client.QueryAll(cmd.Context(), "", "", func(it *taskcorev1.Item) error {
		for _, v := range fn(it) {
			counts[v]++
		}
		return nil
	}); err != nil {
		return err
	}
	names := make([]string, 0, len(counts))
	for n := range counts {
		names = append(names, n)
	}
	sort.Strings(names)

	if a.json {
		for _, n := range names {
			fmt.Fprintf(cmd.OutOrStdout(), "{\"value\":%q,\"count\":%d}\n", n, counts[n])
		}
		return nil
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
	for _, n := range names {
		fmt.Fprintf(w, "%s\t%d\n", n, counts[n])
	}
	return w.Flush()
}
