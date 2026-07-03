package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"gopkg.in/yaml.v3"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/daemon"
)

// withRules dials a dedicated connection for RuleService calls. The
// taskclient SDK does not expose RuleService yet; the root command's dial
// already verified daemon liveness, so this second (lazy) connection is
// plumbing, not a second handshake.
func withRules(cmd *cobra.Command, a *app, fn func(ctx context.Context, rc taskcorev1.RuleServiceClient) error) error {
	conn, err := grpc.NewClient("unix://"+daemon.SocketPath(a.dir),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			// Same provenance header the SDK attaches (taskclient.clientHeader).
			return invoker(metadata.AppendToOutgoingContext(ctx, "x-task-client", clientName), method, req, reply, cc, opts...)
		}),
	)
	if err != nil {
		return err
	}
	defer conn.Close()
	return fn(cmd.Context(), taskcorev1.NewRuleServiceClient(conn))
}

func newRuleCmd(a *app) *cobra.Command {
	rule := &cobra.Command{
		Use:   "rule",
		Short: "Manage rules (the store is the source of truth; YAML files are import/export)",
	}
	rule.AddCommand(
		newRuleLsCmd(a),
		newRuleShowCmd(a),
		newRuleApplyCmd(a),
		newRuleExportCmd(a),
		newRuleRmCmd(a),
		newRuleToggleCmd(a, false),
		newRuleToggleCmd(a, true),
		newRuleDryrunCmd(a),
		newRuleBackfillCmd(a),
		newRuleTemplateCmd(a),
	)
	return rule
}

// ---- YAML import/export format (DESIGN §7's shape) ----

// ruleFile is the `rules:` document `task rule apply/export` speak. It is a
// projection of taskcore.v1.Rule: `schedule` is the cron string shorthand,
// `do.labels.add/remove` map to add_labels/remove_labels, and durations are
// Go duration strings relative to fire time.
type ruleFile struct {
	Rules []ruleYAML `yaml:"rules"`
}

type ruleYAML struct {
	Name        string  `yaml:"name"`
	Description string  `yaml:"description,omitempty"`
	Disabled    bool    `yaml:"disabled,omitempty"`
	Became      string  `yaml:"became,omitempty"`
	Schedule    string  `yaml:"schedule,omitempty"`
	Where       string  `yaml:"where,omitempty"`
	Do          *doYAML `yaml:"do,omitempty"`
}

type doYAML struct {
	Add bool `yaml:"add,omitempty"`
	// Pointer, deliberately: key absent = leave the project alone,
	// `project: ""` = clear it (mirrors the proto's presence semantics).
	Project   *string     `yaml:"project,omitempty"`
	Labels    *labelsYAML `yaml:"labels,omitempty"`
	DueIn     string      `yaml:"due_in,omitempty"`
	SnoozeFor string      `yaml:"snooze_for,omitempty"`
	Complete  bool        `yaml:"complete,omitempty"`
	Reason    string      `yaml:"reason,omitempty"`
	Reopen    bool        `yaml:"reopen,omitempty"`
}

type labelsYAML struct {
	Add    []string `yaml:"add,omitempty"`
	Remove []string `yaml:"remove,omitempty"`
}

// readRuleFile parses a rules YAML file. Unknown keys are errors, not
// silent no-ops: a typo'd `becam:` must not save a rule that never fires.
func readRuleFile(path string) (*ruleFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var rf ruleFile
	if err := dec.Decode(&rf); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(rf.Rules) == 0 {
		return nil, fmt.Errorf("%s: no rules found (want a top-level `rules:` list)", path)
	}
	return &rf, nil
}

func (y ruleYAML) toProto() (*taskcorev1.Rule, error) {
	r := &taskcorev1.Rule{
		Name:        y.Name,
		Description: y.Description,
		Disabled:    y.Disabled,
		Became:      y.Became,
		Where:       y.Where,
	}
	if y.Schedule != "" {
		r.Schedule = &taskcorev1.Schedule{Cron: y.Schedule}
	}
	if y.Do == nil {
		return r, nil // the server rejects a missing `do` with a clear message
	}
	d := &taskcorev1.RuleActions{
		Add:            y.Do.Add,
		Complete:       y.Do.Complete,
		CompleteReason: y.Do.Reason,
		Reopen:         y.Do.Reopen,
		SetProject:     y.Do.Project,
	}
	if y.Do.Labels != nil {
		d.AddLabels = y.Do.Labels.Add
		d.RemoveLabels = y.Do.Labels.Remove
	}
	if y.Do.DueIn != "" {
		dur, err := time.ParseDuration(y.Do.DueIn)
		if err != nil {
			return nil, fmt.Errorf("rule %q: due_in: %w", y.Name, err)
		}
		d.SetDueIn = durationpb.New(dur)
	}
	if y.Do.SnoozeFor != "" {
		dur, err := time.ParseDuration(y.Do.SnoozeFor)
		if err != nil {
			return nil, fmt.Errorf("rule %q: snooze_for: %w", y.Name, err)
		}
		d.SnoozeFor = durationpb.New(dur)
	}
	r.Do = d
	return r, nil
}

func ruleToYAML(r *taskcorev1.Rule) ruleYAML {
	y := ruleYAML{
		Name:        r.GetName(),
		Description: r.GetDescription(),
		Disabled:    r.GetDisabled(),
		Became:      r.GetBecame(),
		Where:       r.GetWhere(),
	}
	if s := r.GetSchedule(); s != nil {
		y.Schedule = s.GetCron()
	}
	d := r.GetDo()
	if d == nil {
		return y
	}
	do := &doYAML{
		Add:      d.GetAdd(),
		Complete: d.GetComplete(),
		Reason:   d.GetCompleteReason(),
		Reopen:   d.GetReopen(),
		Project:  d.SetProject,
	}
	if len(d.GetAddLabels()) > 0 || len(d.GetRemoveLabels()) > 0 {
		do.Labels = &labelsYAML{Add: d.GetAddLabels(), Remove: d.GetRemoveLabels()}
	}
	if dd := d.GetSetDueIn(); dd != nil {
		do.DueIn = dd.AsDuration().String()
	}
	if dd := d.GetSnoozeFor(); dd != nil {
		do.SnoozeFor = dd.AsDuration().String()
	}
	y.Do = do
	return y
}

// ---- display helpers ----

// ruleTrigger renders the trigger column: `became: <expr>` (truncated for
// tables when wide is false) or `cron: <spec>`.
func ruleTrigger(r *taskcorev1.Rule, wide bool) string {
	if s := r.GetSchedule(); s != nil {
		return "cron: " + s.GetCron()
	}
	expr := r.GetBecame()
	if !wide {
		expr = truncateExpr(expr, 60)
	}
	return "became: " + expr
}

func truncateExpr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

// ruleActionsSummary renders the `do:` block compactly, in the engine's
// application order: "add, +code, -stale, project=reviews, complete".
func ruleActionsSummary(d *taskcorev1.RuleActions) string {
	var parts []string
	if d.GetAdd() {
		parts = append(parts, "add")
	}
	for _, l := range d.GetAddLabels() {
		parts = append(parts, "+"+l)
	}
	for _, l := range d.GetRemoveLabels() {
		parts = append(parts, "-"+l)
	}
	if d.SetProject != nil {
		parts = append(parts, "project="+d.GetSetProject())
	}
	if dd := d.GetSetDueIn(); dd != nil {
		parts = append(parts, "due_in="+dd.AsDuration().String())
	}
	if dd := d.GetSnoozeFor(); dd != nil {
		parts = append(parts, "snooze_for="+dd.AsDuration().String())
	}
	if d.GetComplete() {
		parts = append(parts, "complete")
	}
	if d.GetReopen() {
		parts = append(parts, "reopen")
	}
	return strings.Join(parts, ", ")
}

// ---- subcommands ----

func newRuleLsCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List rules in evaluation order",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withRules(cmd, a, func(ctx context.Context, rc taskcorev1.RuleServiceClient) error {
				resp, err := rc.ListRules(ctx, &taskcorev1.ListRulesRequest{})
				if err != nil {
					return err
				}
				if a.json {
					for _, r := range resp.GetRules() {
						if err := printProto(cmd.OutOrStdout(), r); err != nil {
							return err
						}
					}
					return nil
				}
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
				fmt.Fprintln(w, "NAME\tTRIGGER\tACTIONS\tDISABLED")
				for _, r := range resp.GetRules() {
					disabled := ""
					if r.GetDisabled() {
						disabled = "yes"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
						r.GetName(), ruleTrigger(r, false), ruleActionsSummary(r.GetDo()), disabled)
				}
				return w.Flush()
			})
		},
	}
}

func newRuleShowCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show one rule in full",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withRules(cmd, a, func(ctx context.Context, rc taskcorev1.RuleServiceClient) error {
				resp, err := rc.GetRule(ctx, &taskcorev1.GetRuleRequest{Name: args[0]})
				if err != nil {
					return err
				}
				r := resp.GetRule()
				if a.json {
					return printProto(cmd.OutOrStdout(), r)
				}
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
				row := func(k, v string) { fmt.Fprintf(w, "%s\t%s\n", k, v) }
				row("name:", r.GetName())
				if d := r.GetDescription(); d != "" {
					row("description:", d)
				}
				if s := r.GetSchedule(); s != nil {
					row("cron:", s.GetCron())
					row("where:", r.GetWhere())
				} else {
					row("became:", r.GetBecame())
				}
				row("do:", ruleActionsSummary(r.GetDo()))
				row("position:", fmt.Sprintf("%d", r.GetPosition()))
				if r.GetDisabled() {
					row("disabled:", "yes")
				}
				return w.Flush()
			})
		},
	}
}

func newRuleApplyCmd(a *app) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "apply -f rules.yaml",
		Short: "Upsert every rule in a YAML file, in file order",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rf, err := readRuleFile(file)
			if err != nil {
				return err
			}
			return withRules(cmd, a, func(ctx context.Context, rc taskcorev1.RuleServiceClient) error {
				for _, y := range rf.Rules {
					r, err := y.toProto()
					if err != nil {
						return err
					}
					// Keep an existing rule's evaluation slot: saving with
					// position 0 appends, which would shuffle re-applied
					// rules to the end and break apply's idempotence.
					if cur, err := rc.GetRule(ctx, &taskcorev1.GetRuleRequest{Name: r.GetName()}); err == nil {
						r.Position = cur.GetRule().GetPosition()
					} else if status.Code(err) != codes.NotFound {
						return err
					}
					saved, err := rc.SaveRule(ctx, &taskcorev1.SaveRuleRequest{Rule: r})
					if err != nil {
						return err
					}
					if a.json {
						if err := printProto(cmd.OutOrStdout(), saved.GetRule()); err != nil {
							return err
						}
						continue
					}
					fmt.Fprintf(cmd.OutOrStdout(), "saved %s\n", saved.GetRule().GetName())
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "rules YAML file (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newRuleExportCmd(a *app) *cobra.Command {
	var outPath string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export all rules as YAML, in evaluation order (round-trips through apply)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withRules(cmd, a, func(ctx context.Context, rc taskcorev1.RuleServiceClient) error {
				resp, err := rc.ListRules(ctx, &taskcorev1.ListRulesRequest{})
				if err != nil {
					return err
				}
				rf := ruleFile{}
				for _, r := range resp.GetRules() {
					rf.Rules = append(rf.Rules, ruleToYAML(r))
				}
				raw, err := yaml.Marshal(rf)
				if err != nil {
					return err
				}
				var w io.Writer = cmd.OutOrStdout()
				if outPath != "" {
					f, err := os.Create(outPath)
					if err != nil {
						return err
					}
					defer f.Close()
					w = f
				}
				if _, err := w.Write(raw); err != nil {
					return err
				}
				if outPath != "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "exported %d rules to %s\n", len(rf.Rules), outPath)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&outPath, "output", "o", "", "write to FILE instead of stdout")
	return cmd
}

func newRuleRmCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Delete a rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withRules(cmd, a, func(ctx context.Context, rc taskcorev1.RuleServiceClient) error {
				if _, err := rc.DeleteRule(ctx, &taskcorev1.DeleteRuleRequest{Name: args[0]}); err != nil {
					return err
				}
				if !a.json {
					fmt.Fprintf(cmd.OutOrStdout(), "deleted rule %s\n", args[0])
				}
				return nil
			})
		},
	}
}

// newRuleToggleCmd builds `rule enable` / `rule disable`: get, flip, save
// (the round trip through GetRule keeps the rule's evaluation position).
func newRuleToggleCmd(a *app, disable bool) *cobra.Command {
	verb := "enable"
	if disable {
		verb = "disable"
	}
	return &cobra.Command{
		Use:   verb + " <name>",
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " a rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withRules(cmd, a, func(ctx context.Context, rc taskcorev1.RuleServiceClient) error {
				resp, err := rc.GetRule(ctx, &taskcorev1.GetRuleRequest{Name: args[0]})
				if err != nil {
					return err
				}
				r := resp.GetRule()
				r.Disabled = disable
				saved, err := rc.SaveRule(ctx, &taskcorev1.SaveRuleRequest{Rule: r})
				if err != nil {
					return err
				}
				if a.json {
					return printProto(cmd.OutOrStdout(), saved.GetRule())
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%sd %s\n", verb, saved.GetRule().GetName())
				return nil
			})
		},
	}
}

func newRuleDryrunCmd(a *app) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "dryrun [<name>]",
		Short: "Preview what a rule's condition matches right now (no writes)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (file == "") == (len(args) == 0) {
				return fmt.Errorf("give a saved rule name or -f FILE (exactly one)")
			}
			return withRules(cmd, a, func(ctx context.Context, rc taskcorev1.RuleServiceClient) error {
				var r *taskcorev1.Rule
				if file != "" {
					rf, err := readRuleFile(file)
					if err != nil {
						return err
					}
					// First rule of the file, by contract.
					r, err = rf.Rules[0].toProto()
					if err != nil {
						return err
					}
				} else {
					resp, err := rc.GetRule(ctx, &taskcorev1.GetRuleRequest{Name: args[0]})
					if err != nil {
						return err
					}
					r = resp.GetRule()
				}
				resp, err := rc.DryRunRule(ctx, &taskcorev1.DryRunRuleRequest{Rule: r})
				if err != nil {
					return err
				}
				if a.json {
					return printProto(cmd.OutOrStdout(), resp)
				}
				out := cmd.OutOrStdout()
				total := int(resp.GetTotalMatches())
				fmt.Fprintf(out, "%d matching item(s)\n", total)
				shown := min(len(resp.GetMatches()), 20)
				for _, it := range resp.GetMatches()[:shown] {
					fmt.Fprintf(out, "  %s %s\n", shortID(it.GetId()), itemTitle(it))
				}
				if total > shown {
					fmt.Fprintf(out, "  … and %d more\n", total-shown)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "dryrun the FIRST rule of this YAML file instead of a saved rule")
	return cmd
}

func newRuleBackfillCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "backfill <name>",
		Short: "Apply a saved rule once to every currently matching item",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withRules(cmd, a, func(ctx context.Context, rc taskcorev1.RuleServiceClient) error {
				resp, err := rc.BackfillRule(ctx, &taskcorev1.BackfillRuleRequest{Name: args[0]})
				if err != nil {
					return err
				}
				if a.json {
					return printProto(cmd.OutOrStdout(), resp)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "applied to %d items\n", resp.GetApplied())
				return nil
			})
		},
	}
}

func newRuleTemplateCmd(a *app) *cobra.Command {
	tmpl := &cobra.Command{
		Use:   "template",
		Short: "Rule templates shipped by plugins (proposals, never enforced)",
	}

	ls := &cobra.Command{
		Use:   "ls",
		Short: "List available rule templates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withRules(cmd, a, func(ctx context.Context, rc taskcorev1.RuleServiceClient) error {
				resp, err := rc.ListRuleTemplates(ctx, &taskcorev1.ListRuleTemplatesRequest{})
				if err != nil {
					return err
				}
				if a.json {
					for _, t := range resp.GetTemplates() {
						if err := printProto(cmd.OutOrStdout(), t); err != nil {
							return err
						}
					}
					return nil
				}
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
				fmt.Fprintln(w, "PLUGIN\tNAME\tTRIGGER")
				for _, t := range resp.GetTemplates() {
					fmt.Fprintf(w, "%s\t%s\t%s\n",
						t.GetPlugin(), t.GetRule().GetName(), ruleTrigger(t.GetRule(), false))
				}
				return w.Flush()
			})
		},
	}

	apply := &cobra.Command{
		Use:   "apply <plugin>/<name>",
		Short: "Copy a plugin's rule template into your rules",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plugin, name, ok := strings.Cut(args[0], "/")
			if !ok || plugin == "" || name == "" {
				return fmt.Errorf("want <plugin>/<name> (see `task rule template ls`)")
			}
			return withRules(cmd, a, func(ctx context.Context, rc taskcorev1.RuleServiceClient) error {
				resp, err := rc.ListRuleTemplates(ctx, &taskcorev1.ListRuleTemplatesRequest{})
				if err != nil {
					return err
				}
				var r *taskcorev1.Rule
				for _, t := range resp.GetTemplates() {
					if t.GetPlugin() == plugin && t.GetRule().GetName() == name {
						r = t.GetRule()
						break
					}
				}
				if r == nil {
					return fmt.Errorf("no template %q (see `task rule template ls`)", args[0])
				}
				// Templates are copies, not upgrades: refuse to clobber an
				// existing rule the user may have edited.
				if _, err := rc.GetRule(ctx, &taskcorev1.GetRuleRequest{Name: name}); err == nil {
					return fmt.Errorf("rule %q already exists; to import the template under another name, `task rule export`, edit the copy, and `task rule apply` it", name)
				} else if status.Code(err) != codes.NotFound {
					return err
				}
				r.Position = 0 // append at the end of the evaluation order
				saved, err := rc.SaveRule(ctx, &taskcorev1.SaveRuleRequest{Rule: r})
				if err != nil {
					return err
				}
				if a.json {
					return printProto(cmd.OutOrStdout(), saved.GetRule())
				}
				fmt.Fprintf(cmd.OutOrStdout(), "saved %s (from plugin %s)\n", saved.GetRule().GetName(), plugin)
				return nil
			})
		},
	}

	tmpl.AddCommand(ls, apply)
	return tmpl
}
