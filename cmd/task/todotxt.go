package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskpb "github.com/serteal/taskd/gen/task"
)

// todo.txt mapping, applied in both directions:
//
//	x [YYYY-MM-DD]  ↔ completed; the date, when present, is the completion
//	                  date, pinned to 12:00 local
//	(A) (B) (C)     ↔ labels p1 p2 p3; (D)-(Z) are dropped with a warning
//	                  on import
//	+proj           ↔ label project:proj
//	@ctx            ↔ label context:ctx
//	due:YYYY-MM-DD  ↔ due date (end of day on import)
//	label:<name>    ↔ any label the conventions above don't cover
//	other key:value → dropped with a warning on import
//	remaining words ↔ title
type todoLine struct {
	title       string
	labels      []string
	due         time.Time // zero: no due date
	completed   bool
	completedAt time.Time // zero: no completion date on the line
}

var (
	todoPriorityRe = regexp.MustCompile(`^\([A-Z]\)$`)
	todoDateRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	todoTagRe      = regexp.MustCompile(`^([^\s:]+):(\S+)$`)
)

// parseTodoLine maps one todo.txt line to task fields; ok is false for blank
// lines. Unmappable pieces are dropped with a warning on warn.
func parseTodoLine(line string, warn io.Writer) (tl todoLine, ok bool) {
	toks := strings.Fields(line)
	if len(toks) == 0 {
		return todoLine{}, false
	}
	i := 0
	if toks[i] == "x" && len(toks) > 1 {
		tl.completed = true
		i++
		if todoDateRe.MatchString(toks[i]) {
			if d, err := time.ParseInLocation("2006-01-02", toks[i], time.Local); err == nil {
				tl.completedAt = time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, time.Local)
				i++
			}
		}
	}
	if i < len(toks) && todoPriorityRe.MatchString(toks[i]) {
		switch p := toks[i][1]; p {
		case 'A':
			tl.labels = append(tl.labels, "p1")
		case 'B':
			tl.labels = append(tl.labels, "p2")
		case 'C':
			tl.labels = append(tl.labels, "p3")
		default:
			fmt.Fprintf(warn, "warning: dropping priority (%c): only (A)-(C) map to labels\n", p)
		}
		i++
	}
	var words []string
	for ; i < len(toks); i++ {
		tok := toks[i]
		switch {
		case len(tok) > 1 && tok[0] == '+':
			tl.labels = append(tl.labels, "project:"+tok[1:])
		case len(tok) > 1 && tok[0] == '@':
			tl.labels = append(tl.labels, "context:"+tok[1:])
		default:
			m := todoTagRe.FindStringSubmatch(tok)
			if m == nil {
				words = append(words, tok)
				continue
			}
			switch key, val := m[1], m[2]; key {
			case "due":
				d, err := time.ParseInLocation("2006-01-02", val, time.Local)
				if err != nil {
					fmt.Fprintf(warn, "warning: dropping tag %q: not a YYYY-MM-DD date\n", tok)
					continue
				}
				tl.due = time.Date(d.Year(), d.Month(), d.Day(), 23, 59, 59, 0, time.Local)
			case "label":
				tl.labels = append(tl.labels, val)
			default:
				fmt.Fprintf(warn, "warning: dropping unknown tag %q\n", tok)
			}
		}
	}
	tl.title = strings.Join(words, " ")
	return tl, true
}

// formatTodoLine is the inverse mapping. Only the first p1/p2/p3 label
// becomes the (A)/(B)/(C) prefix; a line can carry one priority, so any
// further ones fall through to trailing label: tags (which round-trip).
func formatTodoLine(tl todoLine) string {
	var parts []string
	if tl.completed {
		parts = append(parts, "x")
		if !tl.completedAt.IsZero() {
			parts = append(parts, tl.completedAt.In(time.Local).Format("2006-01-02"))
		}
	}
	var priority string
	var projects, contexts, plain []string
	for _, l := range tl.labels {
		switch {
		case priority == "" && (l == "p1" || l == "p2" || l == "p3"):
			priority = "(" + string(rune('A'+l[1]-'1')) + ")"
		case strings.HasPrefix(l, "project:"):
			projects = append(projects, "+"+strings.TrimPrefix(l, "project:"))
		case strings.HasPrefix(l, "context:"):
			contexts = append(contexts, "@"+strings.TrimPrefix(l, "context:"))
		default:
			plain = append(plain, "label:"+l)
		}
	}
	if priority != "" {
		parts = append(parts, priority)
	}
	if tl.title != "" {
		parts = append(parts, tl.title)
	}
	parts = append(parts, projects...)
	parts = append(parts, contexts...)
	if !tl.due.IsZero() {
		parts = append(parts, "due:"+tl.due.In(time.Local).Format("2006-01-02"))
	}
	parts = append(parts, plain...)
	return strings.Join(parts, " ")
}

func taskToTodoLine(t *taskpb.Task) todoLine {
	tl := todoLine{title: t.GetTitle(), labels: t.GetLabels()}
	if d := t.GetDueTime(); d != nil {
		tl.due = d.AsTime()
	}
	if c := t.GetCompletedTime(); c != nil {
		tl.completed = true
		tl.completedAt = c.AsTime()
	}
	return tl
}

func newImportCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import tasks from external formats",
	}
	cmd.AddCommand(newImportTodotxtCmd(a))
	return cmd
}

func newImportTodotxtCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "todotxt FILE",
		Short: "Import tasks from a todo.txt file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()

			ctx := cmd.Context()
			warn := cmd.ErrOrStderr()
			n := 0
			sc := bufio.NewScanner(f)
			for lineNo := 1; sc.Scan(); lineNo++ {
				tl, ok := parseTodoLine(sc.Text(), warn)
				if !ok {
					continue
				}
				req := &taskpb.CreateTaskRequest{Title: tl.title, Labels: tl.labels}
				if !tl.due.IsZero() {
					req.DueTime = timestamppb.New(tl.due)
				}
				res, err := a.client().CreateTask(ctx, connect.NewRequest(req))
				if err != nil {
					return fmt.Errorf("line %d: %w", lineNo, err)
				}
				// CreateTask can't create completed tasks; a second masked
				// write sets completed_time.
				if tl.completed {
					at := tl.completedAt
					if at.IsZero() {
						at = time.Now()
					}
					if _, err := a.client().UpdateTask(ctx, connect.NewRequest(&taskpb.UpdateTaskRequest{
						Id:         res.Msg.GetTask().GetId(),
						UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"completed_time"}},
						Task:       &taskpb.Task{CompletedTime: timestamppb.New(at)},
					})); err != nil {
						return fmt.Errorf("line %d: complete: %w", lineNo, err)
					}
				}
				n++
			}
			if err := sc.Err(); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "imported %d tasks\n", n)
			return nil
		},
	}
}

func newExportCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export tasks to external formats",
	}
	cmd.AddCommand(newExportTodotxtCmd(a))
	return cmd
}

func newExportTodotxtCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "todotxt [FILE]",
		Short: "Export all tasks as todo.txt (active first, then completed)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := a.out
			var f *os.File
			if len(args) == 1 {
				var err error
				f, err = os.Create(args[0])
				if err != nil {
					return err
				}
				out = f
			}
			w := bufio.NewWriter(out)
			for _, done := range []bool{false, true} {
				tasks, err := listAll(cmd.Context(), a.client(), &taskpb.TaskFilter{Completed: boolPtr(done)}, "created asc", 0)
				if err != nil {
					return err
				}
				for _, t := range tasks {
					fmt.Fprintln(w, formatTodoLine(taskToTodoLine(t)))
				}
			}
			if err := w.Flush(); err != nil {
				return err
			}
			if f != nil {
				return f.Close()
			}
			return nil
		},
	}
}
