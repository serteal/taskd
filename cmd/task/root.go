package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/daemon"
	"todoapp/internal/server"
	"todoapp/pkg/taskclient"
)

// clientName identifies this CLI in write provenance and Ping handshakes.
const clientName = "task-cli"

// app carries the global flags and the dialed client through a single
// command invocation. Every invocation builds a fresh root (and app), so
// tests can run commands side by side.
type app struct {
	dir  string
	json bool

	client *taskclient.Client
}

func newRootCmd() *cobra.Command {
	a := &app{}
	root := &cobra.Command{
		Use:           "task",
		Short:         "task tracks your todos via the taskd daemon",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if skipsClient(cmd) {
				return nil
			}
			return a.dial(cmd)
		},
		PersistentPostRunE: func(*cobra.Command, []string) error {
			if a.client != nil {
				return a.client.Close()
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.PersistentFlags().StringVar(&a.dir, "dir", daemon.DefaultDir(),
		"data directory (socket + database); $TASKD_DIR changes the default")
	root.PersistentFlags().BoolVar(&a.json, "json", false,
		"protojson line output for scripting")

	root.AddCommand(
		newAddCmd(a),
		newLsCmd(a),
		newShowCmd(a),
		newDoneCmd(a),
		newReopenCmd(a),
		newEditCmd(a),
		newRmCmd(a),
		newViewCmd(a),
		newRuleCmd(a),
		newRenameCmd(a),
		newLinkCmd(a),
		newPendingCmd(a),
		newRetryCmd(a),
		newDiscardCmd(a),
		newLabelsCmd(a),
		newProjectsCmd(a),
		newWatchCmd(a),
		newExportCmd(a),
		newImportCmd(a),
		newBackupCmd(a),
		newDaemonCmd(a),
	)
	return root
}

// skipsClient reports whether cmd runs without dialing the daemon: the bare
// root (help), cobra's built-ins, and the daemon lifecycle commands, which
// manage the daemon rather than assume it.
func skipsClient(cmd *cobra.Command) bool {
	if !cmd.HasParent() {
		return true
	}
	switch cmd.Name() {
	case "help", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return true
	}
	for p := cmd; p != nil; p = p.Parent() {
		if p.Name() == "daemon" {
			return true
		}
	}
	return false
}

// dial connects to the daemon, verifies liveness with Ping, and warns on
// version skew. Ping doubles as the connection probe because gRPC dials
// lazily.
func (a *app) dial(cmd *cobra.Command) error {
	sock := daemon.SocketPath(a.dir)
	cl, err := taskclient.Dial(cmd.Context(), sock, clientName)
	if err != nil {
		return connectErr(sock, err)
	}
	pingCtx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
	defer cancel()
	resp, err := cl.Ping(pingCtx)
	if err != nil {
		cl.Close()
		return connectErr(sock, err)
	}
	if v := resp.GetDaemonVersion(); v != server.Version {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"warning: daemon version %s differs from CLI version %s\n", v, server.Version)
	}
	a.client = cl
	return nil
}

func connectErr(sock string, err error) error {
	return fmt.Errorf("cannot reach taskd at %s: %v\nhint: start it with `task daemon run` (foreground) or `task daemon start`", sock, err)
}

// printProto writes one protojson line — the --json output format.
func printProto(w io.Writer, m proto.Message) error {
	b, err := protojson.Marshal(m)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

// shortID is the id prefix used in human output; the server resolves unique
// prefixes on every RPC. Ten characters cover a ULID's full millisecond
// timestamp, so only items minted in the same millisecond (batch imports)
// share it — listings extend past ten as needed via uniquePrefixes.
const shortIDLen = 10

func shortID(id string) string {
	if len(id) > shortIDLen {
		return id[:shortIDLen]
	}
	return id
}

// uniquePrefixes returns a display prefix per id, extending beyond
// shortIDLen only where ids in this set collide at that length.
func uniquePrefixes(ids []string) map[string]string {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	lcp := func(a, b string) int {
		n := 0
		for n < len(a) && n < len(b) && a[n] == b[n] {
			n++
		}
		return n
	}
	need := make(map[string]int, len(sorted))
	for i, id := range sorted {
		n := 0
		if i > 0 {
			n = max(n, lcp(id, sorted[i-1]))
		}
		if i < len(sorted)-1 {
			n = max(n, lcp(id, sorted[i+1]))
		}
		need[id] = min(max(n+1, shortIDLen), len(id))
	}
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		out[id] = id[:need[id]]
	}
	return out
}

// itemTitle is the display title: the user's override, else the mirror's.
func itemTitle(it *taskcorev1.Item) string {
	if t := it.GetTodo().GetTitleOverride(); t != "" {
		return t
	}
	return it.GetMirror().GetTitle()
}

// humanDue renders a due timestamp by calendar-day distance: "today", "2d",
// "-3d" for overdue, "" for none.
func humanDue(ts *timestamppb.Timestamp, now time.Time) string {
	if ts == nil {
		return ""
	}
	loc := now.Location()
	d := ts.AsTime().In(loc)
	day := func(t time.Time) time.Time {
		y, m, dd := t.Date()
		return time.Date(y, m, dd, 0, 0, 0, 0, loc)
	}
	days := int(math.Round(day(d).Sub(day(now)).Hours() / 24))
	if days == 0 {
		return "today"
	}
	return fmt.Sprintf("%dd", days)
}

// celQuote renders s as a CEL string literal (Go and CEL share the relevant
// escape syntax).
func celQuote(s string) string {
	return fmt.Sprintf("%q", s)
}

// andFilter AND-composes CEL fragments, parenthesizing each.
func andFilter(frags ...string) string {
	var parts []string
	for _, f := range frags {
		if strings.TrimSpace(f) == "" {
			continue
		}
		parts = append(parts, "("+f+")")
	}
	return strings.Join(parts, " && ")
}
