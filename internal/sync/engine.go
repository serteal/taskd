package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/clock"
	"todoapp/internal/feed"
	"todoapp/internal/store"
)

// DefaultGrace is the tombstone grace period: how long an item stays merely
// "missing" after vanishing from snapshots before its mirror is frozen as
// stale. Disappearance is usually scope drift or a moved object — never a
// reason to lose data.
const DefaultGrace = 48 * time.Hour

type Engine struct {
	st    store.Store
	hub   *feed.Hub
	clk   clock.Clock
	ids   clock.IDGen
	log   *slog.Logger
	grace time.Duration
}

func NewEngine(st store.Store, hub *feed.Hub, clk clock.Clock, ids clock.IDGen, log *slog.Logger, grace time.Duration) *Engine {
	if grace <= 0 {
		grace = DefaultGrace
	}
	if log == nil {
		log = slog.Default()
	}
	return &Engine{st: st, hub: hub, clk: clk, ids: ids, log: log, grace: grace}
}

// Stats summarizes one reconcile cycle.
type Stats struct {
	Created, Updated, Recovered, Missing, Stale, Relations, Skipped int
}

func (s Stats) zero() bool { return s == Stats{} }

// RunInstance reconciles src every poll interval until ctx ends; the first
// cycle runs immediately. Errors are logged, never fatal: a broken remote
// heals on a later cycle.
func (e *Engine) RunInstance(ctx context.Context, src Source, poll time.Duration) {
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		stats, err := e.ReconcileOnce(ctx, src)
		switch {
		case err != nil && ctx.Err() == nil:
			e.log.Warn("sync cycle failed", "instance", src.Instance(), "err", err)
		case !stats.zero():
			e.log.Info("sync cycle", "instance", src.Instance(),
				"created", stats.Created, "updated", stats.Updated, "recovered", stats.Recovered,
				"missing", stats.Missing, "stale", stats.Stale, "relations", stats.Relations, "skipped", stats.Skipped)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ReconcileOnce performs one snapshot-diff cycle: the smart half of the
// dumb-connector contract. A snapshot error aborts the whole cycle — an
// unreachable remote must never read as an empty remote, or the instance
// would mass-tombstone.
func (e *Engine) ReconcileOnce(ctx context.Context, src Source) (Stats, error) {
	var stats Stats
	instance := src.Instance()

	remotes, err := src.Snapshot(ctx)
	if err != nil {
		return stats, fmt.Errorf("snapshot: %w", err)
	}

	// Index the snapshot; a connector emitting duplicate or anonymous ids is
	// buggy — skip those entries loudly rather than corrupting state.
	byExt := make(map[string]*pluginv1.RemoteItem, len(remotes))
	for _, r := range remotes {
		switch {
		case r.GetExternalId() == "" || r.GetKind() == "":
			stats.Skipped++
			e.log.Warn("connector emitted item without external_id/kind", "instance", instance)
		case byExt[r.GetExternalId()] != nil:
			stats.Skipped++
			e.log.Warn("connector emitted duplicate external_id", "instance", instance, "external_id", r.GetExternalId())
		default:
			byExt[r.GetExternalId()] = r
		}
	}

	existing, err := e.st.ListInstanceItems(ctx, instance)
	if err != nil {
		return stats, err
	}
	localByExt := make(map[string]*taskcorev1.Item, len(existing))
	for _, it := range existing {
		localByExt[it.GetMirror().GetLink().GetExternalId()] = it
	}

	now := e.clk.Now()
	prov := &taskcorev1.Provenance{Source: taskcorev1.ProvenanceSource_PROVENANCE_SOURCE_SYNC, Ref: instance}
	idByExt := make(map[string]string, len(byExt)) // for the relations pass

	for ext, r := range byExt {
		local := localByExt[ext]
		if local == nil {
			item := &taskcorev1.Item{
				Id:             e.ids.NewID(),
				Kind:           r.GetKind(),
				Mirror:         mirrorFrom(r, instance, now),
				MirrorRevision: 1,
				CreatedAt:      timestamppb.New(now),
				UpdatedAt:      timestamppb.New(now),
			}
			evt, err := e.st.CreateItem(ctx, item, prov)
			if err != nil {
				return stats, fmt.Errorf("create %s: %w", ext, err)
			}
			e.hub.Publish(evt)
			idByExt[ext] = item.GetId()
			stats.Created++
			continue
		}

		idByExt[ext] = local.GetId()
		wasMissing := local.GetMirror().GetMissingSince() != nil || local.GetMirror().GetStale()
		_, evt, err := e.st.MutateItem(ctx, local.GetId(), prov, func(it *taskcorev1.Item) error {
			applyRemote(it.GetMirror(), r, now)
			return nil
		})
		if err != nil {
			return stats, fmt.Errorf("update %s: %w", ext, err)
		}
		if evt != nil { // empty diff = echo/no-op, silenced by value
			e.hub.Publish(evt)
			if wasMissing {
				stats.Recovered++
			} else {
				stats.Updated++
			}
		}
	}

	// Disappearances: mark missing, then freeze as stale after the grace
	// period. Never delete — the item may carry the user's todo, and the
	// remote may come back.
	for ext, local := range localByExt {
		if byExt[ext] != nil {
			continue
		}
		mirror := local.GetMirror()
		switch {
		case mirror.GetMissingSince() == nil:
			_, evt, err := e.st.MutateItem(ctx, local.GetId(), prov, func(it *taskcorev1.Item) error {
				it.GetMirror().MissingSince = timestamppb.New(now)
				return nil
			})
			if err != nil {
				return stats, fmt.Errorf("mark missing %s: %w", ext, err)
			}
			if evt != nil {
				e.hub.Publish(evt)
				stats.Missing++
			}
		case !mirror.GetStale() && now.Sub(mirror.GetMissingSince().AsTime()) > e.grace:
			_, evt, err := e.st.MutateItem(ctx, local.GetId(), prov, func(it *taskcorev1.Item) error {
				it.GetMirror().Stale = true
				return nil
			})
			if err != nil {
				return stats, fmt.Errorf("mark stale %s: %w", ext, err)
			}
			if evt != nil {
				e.hub.Publish(evt)
				stats.Stale++
			}
		}
	}

	// Relations pass: wire parent hints (series ← instances) now that every
	// external id has an item id. Connectors never see item ids.
	for ext, r := range byExt {
		parentExt := r.GetParentExternalId()
		if parentExt == "" {
			continue
		}
		parentID := idByExt[parentExt]
		if parentID == "" {
			stats.Skipped++
			e.log.Warn("parent hint refers to an external_id absent from the snapshot",
				"instance", instance, "external_id", ext, "parent", parentExt)
			continue
		}
		relType := r.GetParentRelation()
		if relType == taskcorev1.RelationType_RELATION_TYPE_UNSPECIFIED {
			relType = taskcorev1.RelationType_RELATION_TYPE_INSTANCE_OF
		}
		_, evt, err := e.st.MutateItem(ctx, idByExt[ext], prov, func(it *taskcorev1.Item) error {
			for _, rel := range it.GetRelations() {
				if rel.GetType() == relType && rel.GetTargetId() == parentID {
					return nil // already wired; no-op suppression handles the rest
				}
			}
			it.Relations = append(it.Relations, &taskcorev1.Relation{Type: relType, TargetId: parentID})
			return nil
		})
		if err != nil {
			return stats, fmt.Errorf("wire relation %s: %w", ext, err)
		}
		if evt != nil {
			e.hub.Publish(evt)
			stats.Relations++
		}
	}

	return stats, nil
}

// mirrorFrom builds a fresh Mirror from a RemoteItem.
func mirrorFrom(r *pluginv1.RemoteItem, instance string, now time.Time) *taskcorev1.Mirror {
	m := &taskcorev1.Mirror{
		Link: &taskcorev1.ExternalLink{
			ConnectorInstance: instance,
			ExternalId:        r.GetExternalId(),
		},
	}
	applyRemote(m, r, now)
	return m
}

// applyRemote overwrites the mirror's remote-owned fields from the snapshot.
// The todo layer and relations are untouched by construction: this function
// only ever receives the mirror. last_synced_at is bookkeeping the differ
// ignores, so setting it here persists only alongside a real change.
func applyRemote(m *taskcorev1.Mirror, r *pluginv1.RemoteItem, now time.Time) {
	m.Title = r.GetTitle()
	m.State = r.GetState()
	m.Data = r.GetData()
	m.Stale = false
	m.MissingSince = nil
	m.Link.ExternalUrl = r.GetUrl()
	m.Link.Etag = r.GetEtag()
	m.Link.LastSyncedAt = timestamppb.New(now)
}
