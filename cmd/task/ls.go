package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskpb "todoapp/gen/task"
	"todoapp/gen/task/taskconnect"
)

func newLsCmd(a *app) *cobra.Command {
	var (
		labels    []string
		all       bool
		completed bool
		source    string
		dueBefore string
		dueAfter  string
		noDue     bool
		hasDue    bool
		order     string
		limit     int
	)
	cmd := &cobra.Command{
		Use:   "ls [TEXT...]",
		Short: "List tasks (active only by default)",
		RunE: func(cmd *cobra.Command, args []string) error {
			now := time.Now()
			filter := &taskpb.TaskFilter{
				LabelsAll: labels,
				Text:      strings.Join(args, " "),
			}
			switch {
			case all:
			case completed:
				filter.Completed = boolPtr(true)
			default:
				filter.Completed = boolPtr(false)
			}
			if cmd.Flags().Changed("source") {
				filter.Source = &source
			}
			if dueBefore != "" {
				t, err := parseWhen(dueBefore, now)
				if err != nil {
					return err
				}
				filter.DueBefore = timestamppb.New(t)
			}
			if dueAfter != "" {
				t, err := parseWhen(dueAfter, now)
				if err != nil {
					return err
				}
				filter.DueAfter = timestamppb.New(t)
			}
			if noDue {
				filter.HasDue = boolPtr(false)
			}
			if hasDue {
				filter.HasDue = boolPtr(true)
			}

			orderBy, err := parseOrder(order)
			if err != nil {
				return err
			}
			tasks, err := listAll(cmd.Context(), a.client(), filter, orderBy, limit)
			if err != nil {
				return err
			}
			if a.json {
				return printJSONArray(a.out, tasks)
			}

			color := isTerminal(a.out)
			w := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
			for _, t := range tasks {
				mark := ""
				if t.GetCompletedTime() != nil {
					mark = "✓"
				}
				tags := make([]string, len(t.GetLabels()))
				for i, l := range t.GetLabels() {
					tags[i] = "#" + l
				}
				due := ""
				if dt := t.GetDueTime(); dt != nil {
					s, overdue := humanDue(dt.AsTime().Local(), now)
					if overdue && color {
						s = "\x1b[31m" + s + "\x1b[0m"
					}
					due = s
				}
				src := ""
				if t.GetSource() != "" {
					src = "[" + t.GetSource() + "]"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					shortID(t.GetId()), mark, t.GetTitle(), strings.Join(tags, " "), due, src)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringArrayVarP(&labels, "label", "l", nil, "require this label (repeatable, all must match)")
	cmd.Flags().BoolVar(&all, "all", false, "include completed tasks")
	cmd.Flags().BoolVar(&completed, "completed", false, "only completed tasks")
	cmd.Flags().StringVar(&source, "source", "", "only tasks synced from this source")
	cmd.Flags().StringVar(&dueBefore, "due-before", "", "only tasks due before WHEN")
	cmd.Flags().StringVar(&dueAfter, "due-after", "", "only tasks due at or after WHEN")
	cmd.Flags().BoolVar(&noDue, "no-due", false, "only tasks without a due date")
	cmd.Flags().BoolVar(&hasDue, "has-due", false, "only tasks with a due date")
	cmd.Flags().StringVar(&order, "order", "due:asc", "sort order: created|updated|due|title[:asc|:desc]")
	cmd.Flags().IntVar(&limit, "limit", 200, "maximum tasks to list")
	cmd.MarkFlagsMutuallyExclusive("all", "completed")
	cmd.MarkFlagsMutuallyExclusive("no-due", "has-due")
	return cmd
}

// parseOrder maps the CLI's field[:dir] form to the server's "field dir".
func parseOrder(s string) (string, error) {
	field, dir, hasDir := strings.Cut(s, ":")
	switch field {
	case "created", "updated", "due", "title":
	default:
		return "", fmt.Errorf("invalid order %q: want created|updated|due|title, optionally :asc or :desc", s)
	}
	if !hasDir {
		return field, nil // bare field is ascending server-side
	}
	switch dir {
	case "asc", "desc":
		return field + " " + dir, nil
	}
	return "", fmt.Errorf("invalid order direction %q: want asc or desc", dir)
}

// listAll pages through ListTasks until limit tasks are collected (limit <= 0
// means all).
func listAll(ctx context.Context, tc taskconnect.TaskServiceClient, filter *taskpb.TaskFilter, orderBy string, limit int) ([]*taskpb.Task, error) {
	var tasks []*taskpb.Task
	token := ""
	for {
		size := 1000
		if limit > 0 {
			size = min(limit-len(tasks), 1000)
			if size <= 0 {
				break
			}
		}
		res, err := tc.ListTasks(ctx, connect.NewRequest(&taskpb.ListTasksRequest{
			Filter:    filter,
			OrderBy:   orderBy,
			PageSize:  int32(size),
			PageToken: token,
		}))
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, res.Msg.GetTasks()...)
		token = res.Msg.GetNextPageToken()
		if token == "" {
			break
		}
	}
	if limit > 0 && len(tasks) > limit {
		tasks = tasks[:limit]
	}
	return tasks, nil
}

// humanDue renders a due time relative to now; overdue reports whether the
// deadline has passed so callers can colorize.
func humanDue(due, now time.Time) (s string, overdue bool) {
	if due.Before(now) {
		return "overdue", true
	}
	dy, dm, dd := due.Date()
	ny, nm, nd := now.Date()
	ty, tm, td := now.AddDate(0, 0, 1).Date()
	switch {
	case dy == ny && dm == nm && dd == nd:
		return "today", false
	case dy == ty && dm == tm && dd == td:
		return "tomorrow", false
	case dy == ny:
		return due.Format("Jan 2"), false
	}
	return due.Format("Jan 2 2006"), false
}

func printJSONArray(w io.Writer, tasks []*taskpb.Task) error {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, t := range tasks {
		if i > 0 {
			buf.WriteByte(',')
		}
		b, err := protojson.Marshal(t)
		if err != nil {
			return err
		}
		buf.Write(b)
	}
	buf.WriteString("]\n")
	_, err := w.Write(buf.Bytes())
	return err
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func boolPtr(b bool) *bool { return &b }
