package rules

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"google.golang.org/protobuf/proto"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/clock"
	"todoapp/internal/feed"
	"todoapp/internal/query"
	"todoapp/internal/store"
)

const (
	// cursorKey is the engine's durable position in the event log (meta KV).
	// Persisted after each drained batch: crash-replay re-fires actions,
	// which are idempotent, so at-least-once is safe.
	cursorKey = "rules.cursor"
	// maxDepth bounds rule cascades per originating event. Rule A may react
	// to rule B's changes, but a ping-pong pair dies here instead of
	// spinning forever.
	maxDepth = 10
	// tickInterval paces schedule checks and acts as a fallback drain.
	tickInterval = 30 * time.Second
)

type Engine struct {
	st  store.Store
	hub *feed.Hub
	qe  *query.Engine
	clk clock.Clock
	log *slog.Logger

	// matchers caches compiled conditions keyed by rule name; entries are
	// invalidated by comparing the serialized rule.
	matchers map[string]*compiledRule
	// depth tracks cascade depth per event cursor (in-memory: the engine is
	// the only rule executor and processes serially; a crash resets depth,
	// which at worst lets a capped cascade resume — still capped).
	depth map[uint64]int
	// nextFire tracks each schedule rule's next due time. Initialized at
	// engine start; downtime is not caught up (documented).
	nextFire map[string]time.Time
}

type compiledRule struct {
	raw   []byte // serialized rule, for cache invalidation
	match func(*taskcorev1.Item) (bool, error)
}

func NewEngine(st store.Store, hub *feed.Hub, qe *query.Engine, clk clock.Clock, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	return &Engine{
		st: st, hub: hub, qe: qe, clk: clk, log: log,
		matchers: make(map[string]*compiledRule),
		depth:    make(map[uint64]int),
		nextFire: make(map[string]time.Time),
	}
}

// Run drains pending events (catching up on anything written while the
// daemon was down), then loops: waking on published events, ticking
// schedules, until ctx ends. Never returns an error mid-flight — a broken
// rule logs and is skipped; the feed must keep flowing.
func (e *Engine) Run(ctx context.Context) {
	sub := e.hub.Subscribe(256)
	defer sub.Close()

	if _, err := e.DrainOnce(ctx); err != nil && ctx.Err() == nil {
		e.log.Warn("rules: initial drain failed", "err", err)
	}
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-sub.C():
			if !ok {
				// Lagged: the durable cursor makes that harmless — resubscribe
				// and drain from where we left off.
				sub = e.hub.Subscribe(256)
				continue
			}
			// Drain coalesces however many events are pending.
			if _, err := e.DrainOnce(ctx); err != nil && ctx.Err() == nil {
				e.log.Warn("rules: drain failed", "err", err)
			}
		case <-ticker.C:
			if err := e.TickSchedules(ctx, e.clk.Now()); err != nil && ctx.Err() == nil {
				e.log.Warn("rules: schedule tick failed", "err", err)
			}
			if _, err := e.DrainOnce(ctx); err != nil && ctx.Err() == nil {
				e.log.Warn("rules: drain failed", "err", err)
			}
		}
	}
}

// DrainOnce processes every event past the durable cursor, in order, and
// persists the new cursor. Exported for tests and daemon startup.
func (e *Engine) DrainOnce(ctx context.Context) (int, error) {
	cursor, err := e.loadCursor(ctx)
	if err != nil {
		return 0, err
	}
	processed := 0
	for {
		events, err := e.st.ListEvents(ctx, cursor, 200)
		if err != nil {
			return processed, err
		}
		if len(events) == 0 {
			return processed, nil
		}
		rules, err := e.becameRules(ctx)
		if err != nil {
			return processed, err
		}
		for _, ev := range events {
			if err := e.process(ctx, rules, ev); err != nil {
				// A failing rule must not wedge the feed: log, move on.
				e.log.Warn("rules: event processing", "cursor", ev.GetCursor(), "err", err)
			}
			cursor = ev.GetCursor()
			delete(e.depth, cursor)
			processed++
		}
		if err := e.st.SetMeta(ctx, cursorKey, strconv.FormatUint(cursor, 10)); err != nil {
			return processed, err
		}
	}
}

// process runs every enabled became-rule against one event with edge
// semantics: fire iff the condition was false on the before-image and true
// on the after-image.
func (e *Engine) process(ctx context.Context, rules []*taskcorev1.Rule, ev *taskcorev1.Event) error {
	if ev.GetType() == taskcorev1.ChangeType_CHANGE_TYPE_DELETED {
		return nil
	}
	depth := e.depth[ev.GetCursor()]
	if depth >= maxDepth {
		e.log.Warn("rules: cascade depth cap hit; dropping event for rules",
			"item", ev.GetItemId(), "depth", depth)
		return nil
	}

	var before *taskcorev1.Item
	beforeReady := false

	for _, rule := range rules {
		// Self-guard: a rule never fires on changes it caused. Cross-rule
		// cascades are allowed and bounded by the depth cap.
		cb := ev.GetCausedBy()
		if cb.GetSource() == taskcorev1.ProvenanceSource_PROVENANCE_SOURCE_RULE && cb.GetRef() == rule.GetName() {
			continue
		}
		m, err := e.matcher(rule)
		if err != nil {
			e.log.Warn("rules: rule does not compile; skipping", "rule", rule.GetName(), "err", err)
			continue
		}
		after, err := m(ev.GetItem())
		if err != nil || !after {
			continue
		}
		// Condition true after; edge requires false before. CREATED events
		// have no before — the condition was false by definition.
		wasTrue := false
		if ev.GetType() == taskcorev1.ChangeType_CHANGE_TYPE_UPDATED {
			if !beforeReady {
				before, err = feed.BeforeImage(ev)
				if err != nil {
					return fmt.Errorf("before-image: %w", err)
				}
				beforeReady = true
			}
			wasTrue, err = m(before)
			if err != nil {
				continue
			}
		}
		if wasTrue {
			continue // level, not edge: the doorbell did not ring
		}

		evt, err := fire(ctx, e.st, rule, ev.GetItemId(), e.clk.Now())
		if err != nil {
			e.log.Warn("rules: fire failed", "rule", rule.GetName(), "item", ev.GetItemId(), "err", err)
			continue
		}
		if evt != nil {
			e.depth[evt.GetCursor()] = depth + 1
			e.hub.Publish(evt)
			e.log.Info("rule fired", "rule", rule.GetName(), "item", ev.GetItemId(), "depth", depth)
		}
	}
	return nil
}

// TickSchedules fires every schedule rule whose cron slot has come due
// since the last tick, applying its actions to all items matching `where`.
func (e *Engine) TickSchedules(ctx context.Context, now time.Time) error {
	all, err := e.st.ListRules(ctx)
	if err != nil {
		return err
	}
	for _, rule := range all {
		if rule.GetDisabled() || rule.GetSchedule() == nil {
			continue
		}
		sched, err := cronParser.Parse(rule.GetSchedule().GetCron())
		if err != nil {
			continue // Validate prevents this; belt and suspenders
		}
		next, known := e.nextFire[rule.GetName()]
		if !known {
			// First sighting: schedule forward from now. Downtime is not
			// caught up — a missed 8am sweep waits for tomorrow's.
			e.nextFire[rule.GetName()] = sched.Next(now)
			continue
		}
		if now.Before(next) {
			continue
		}
		e.nextFire[rule.GetName()] = sched.Next(now)
		if n, err := e.runScheduled(ctx, rule, now); err != nil {
			e.log.Warn("rules: scheduled run failed", "rule", rule.GetName(), "err", err)
		} else if n > 0 {
			e.log.Info("scheduled rule fired", "rule", rule.GetName(), "items", n)
		}
	}
	return nil
}

func (e *Engine) runScheduled(ctx context.Context, rule *taskcorev1.Rule, now time.Time) (int, error) {
	return e.applyToMatching(ctx, rule, rule.GetWhere(), now)
}

// Backfill applies a saved rule once to every item currently matching its
// condition — the explicit opt-in that keeps rule edits non-retroactive.
func (e *Engine) Backfill(ctx context.Context, name string) (int, error) {
	rule, err := e.st.GetRule(ctx, name)
	if err != nil {
		return 0, err
	}
	cond := rule.GetBecame()
	if rule.GetSchedule() != nil {
		cond = rule.GetWhere()
	}
	return e.applyToMatching(ctx, rule, cond, e.clk.Now())
}

func (e *Engine) applyToMatching(ctx context.Context, rule *taskcorev1.Rule, cond string, now time.Time) (int, error) {
	compiled, err := e.qe.Compile(cond)
	if err != nil {
		return 0, err
	}
	applied := 0
	token := ""
	for {
		res, err := e.st.QueryItems(ctx, store.Query{
			Where: compiled.Where, Args: compiled.Args, Residual: compiled.Residual,
			OrderBy: "created_at", PageSize: 500, PageToken: token,
		})
		if err != nil {
			return applied, err
		}
		for _, it := range res.Items {
			evt, err := fire(ctx, e.st, rule, it.GetId(), now)
			if err != nil {
				return applied, err
			}
			if evt != nil {
				e.hub.Publish(evt)
				applied++
			}
		}
		if res.NextPageToken == "" {
			return applied, nil
		}
		token = res.NextPageToken
	}
}

// becameRules loads enabled edge rules in evaluation order, refreshing the
// matcher cache.
func (e *Engine) becameRules(ctx context.Context) ([]*taskcorev1.Rule, error) {
	all, err := e.st.ListRules(ctx)
	if err != nil {
		return nil, err
	}
	live := make(map[string]bool, len(all))
	out := make([]*taskcorev1.Rule, 0, len(all))
	for _, r := range all {
		live[r.GetName()] = true
		if !r.GetDisabled() && r.GetBecame() != "" {
			out = append(out, r)
		}
	}
	for name := range e.matchers {
		if !live[name] {
			delete(e.matchers, name)
		}
	}
	return out, nil
}

func (e *Engine) matcher(rule *taskcorev1.Rule) (func(*taskcorev1.Item) (bool, error), error) {
	raw, err := proto.Marshal(rule)
	if err != nil {
		return nil, err
	}
	if c, ok := e.matchers[rule.GetName()]; ok && string(c.raw) == string(raw) {
		return c.match, nil
	}
	m, err := e.qe.Matcher(rule.GetBecame())
	if err != nil {
		return nil, err
	}
	e.matchers[rule.GetName()] = &compiledRule{raw: raw, match: m}
	return m, nil
}

func (e *Engine) loadCursor(ctx context.Context) (uint64, error) {
	v, err := e.st.GetMeta(ctx, cursorKey)
	if err != nil {
		return 0, err
	}
	if v == "" {
		// First run: start at the log's tail. Pre-existing history is not
		// retroactively processed — same principle as rule edits.
		latest, err := e.st.LatestCursor(ctx)
		if err != nil {
			return 0, err
		}
		if err := e.st.SetMeta(ctx, cursorKey, strconv.FormatUint(latest, 10)); err != nil {
			return 0, err
		}
		return latest, nil
	}
	return strconv.ParseUint(v, 10, 64)
}
