// Package rules is the rules engine: the programmable wiring between the
// mirror and todo layers. Edge-triggered rules consume the change feed with
// before/after evaluation; scheduled rules sweep current state on a cron
// cadence. All writes carry RULE provenance, are idempotent, and are
// guarded against self-loops and unbounded cascades.
package rules

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/intent"
	"todoapp/internal/query"
	"todoapp/internal/store"
)

// cronParser: standard 5-field cron.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Validate checks a rule end to end: exactly one trigger, compilable
// conditions, parseable cron, and non-empty actions. Shared by RuleService
// (save-time) and the engine (load-time), so a rule that saves is a rule
// that runs.
func Validate(qe *query.Engine, r *taskcorev1.Rule) error {
	if strings.TrimSpace(r.GetName()) == "" {
		return fmt.Errorf("rule needs a name")
	}
	hasBecame := strings.TrimSpace(r.GetBecame()) != ""
	hasSchedule := r.GetSchedule() != nil
	if hasBecame == hasSchedule {
		return fmt.Errorf("rule %q: exactly one of `became` or `schedule` must be set", r.GetName())
	}
	if hasBecame {
		if _, err := qe.Matcher(r.GetBecame()); err != nil {
			return fmt.Errorf("rule %q: became: %w", r.GetName(), err)
		}
		if strings.TrimSpace(r.GetWhere()) != "" {
			return fmt.Errorf("rule %q: `where` belongs to schedule rules; fold it into `became`", r.GetName())
		}
	}
	if hasSchedule {
		if _, err := cronParser.Parse(r.GetSchedule().GetCron()); err != nil {
			return fmt.Errorf("rule %q: cron %q: %w", r.GetName(), r.GetSchedule().GetCron(), err)
		}
		if strings.TrimSpace(r.GetWhere()) == "" {
			return fmt.Errorf("rule %q: schedule rules need a `where` condition", r.GetName())
		}
		if _, err := qe.Matcher(r.GetWhere()); err != nil {
			return fmt.Errorf("rule %q: where: %w", r.GetName(), err)
		}
	}
	if err := validateActions(r.GetDo()); err != nil {
		return fmt.Errorf("rule %q: %w", r.GetName(), err)
	}
	return nil
}

func validateActions(a *taskcorev1.RuleActions) error {
	if a == nil {
		return fmt.Errorf("`do` is required")
	}
	if a.GetComplete() && a.GetReopen() {
		return fmt.Errorf("`complete` and `reopen` are mutually exclusive")
	}
	empty := !a.GetAdd() && !a.GetComplete() && !a.GetReopen() &&
		len(a.GetAddLabels()) == 0 && len(a.GetRemoveLabels()) == 0 &&
		a.SetProject == nil && a.GetSetDueIn() == nil && a.GetSnoozeFor() == nil &&
		a.GetIntent() == ""
	if empty {
		return fmt.Errorf("`do` has no actions")
	}
	if !a.GetComplete() && a.GetCompleteReason() != "" {
		return fmt.Errorf("`complete_reason` without `complete`")
	}
	if n := a.GetIntent(); n != "" && !intent.Known(n) {
		return fmt.Errorf("unknown intent %q (standard intents: rename, add_comment, delete, set_due, set_start, assign, set_priority, set_completed)", n)
	}
	return nil
}

// applyActions mutates the item per the rule's action set, in the fixed
// canonical order: promote, project, labels, due/snooze, complete/reopen.
// Any todo-layer action promotes an un-triaged mirror (acting on an item
// implies triaging it). Every operation is a set — re-applying is a no-op,
// which the store's empty-diff suppression turns into "no event", making
// at-least-once delivery safe.
func applyActions(it *taskcorev1.Item, a *taskcorev1.RuleActions, now time.Time) {
	if it.Todo == nil {
		it.Todo = &taskcorev1.Todo{}
	}
	todo := it.Todo

	if a.SetProject != nil {
		todo.Project = a.GetSetProject()
	}
	if len(a.GetAddLabels()) > 0 || len(a.GetRemoveLabels()) > 0 {
		todo.Labels = editLabels(todo.GetLabels(), a.GetAddLabels(), a.GetRemoveLabels())
	}
	if d := a.GetSetDueIn(); d != nil {
		todo.Due = timestamppb.New(now.Add(d.AsDuration()))
	}
	if d := a.GetSnoozeFor(); d != nil {
		todo.SnoozedUntil = timestamppb.New(now.Add(d.AsDuration()))
	}
	switch {
	case a.GetComplete() && !todo.GetCompleted():
		todo.Completed = true
		todo.CompletedAt = timestamppb.New(now)
		todo.CompletedReason = a.GetCompleteReason()
	case a.GetReopen() && todo.GetCompleted():
		todo.Completed = false
		todo.CompletedAt = nil
		todo.CompletedReason = ""
	}
}

func editLabels(current, add, remove []string) []string {
	seen := make(map[string]bool, len(current)+len(add))
	rm := make(map[string]bool, len(remove))
	for _, l := range remove {
		rm[l] = true
	}
	var out []string
	for _, l := range append(append([]string{}, current...), add...) {
		if rm[l] || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}

// fire applies one rule to one item through the store, returning the event
// (nil when everything was already in the desired state).
func fire(ctx context.Context, st store.Store, rule *taskcorev1.Rule, itemID string, now time.Time) (*taskcorev1.Event, error) {
	prov := &taskcorev1.Provenance{
		Source: taskcorev1.ProvenanceSource_PROVENANCE_SOURCE_RULE,
		Ref:    rule.GetName(),
	}
	_, evt, err := st.MutateItem(ctx, itemID, prov, func(it *taskcorev1.Item) error {
		applyActions(it, rule.GetDo(), now)
		return nil
	})
	return evt, err
}
