package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func newShowCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show ID",
		Short: "Show a task in full",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := resolveTask(cmd.Context(), a.client(), args[0])
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
			return nil
		},
	}
}

func localStamp(ts *timestamppb.Timestamp) string {
	return ts.AsTime().Local().Format("2006-01-02 15:04:05")
}
