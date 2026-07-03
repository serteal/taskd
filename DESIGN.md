# Design: an extensible task tracker

*Status: consolidated design, pre-implementation — 2026-07-03*

## 1. Summary

A local Go daemon (`taskd`) owns a canonical store and exposes everything over
gRPC/protobuf. Every other component — CLI, TUI, web UI, MCP server, and all
extensions — is a gRPC peer. Extensibility is not a plugin API bolted on the
side; it is the only way anything talks to the core.

This is a **centralized personal tracker, not a broker**: it pulls tasks,
bugs, events, and other trackable things from many sources into one place,
with one query language, one rules engine, and one editing surface. Each
tracked item has at most one external source. It does not federate items
across third-party apps.

The core's job is deliberately small: store items, mirror remote objects,
run the rules engine, route write intents, and serve UI contributions. All
semantics about *what tasks mean* live in user configuration (labels,
projects, rules, filters), not in the schema.

**Decisions locked in:**

| Decision | Choice |
|---|---|
| Core language | Go (plugins: any language, via gRPC) |
| Topology | Local daemon, SQLite, unix socket |
| Ownership model | Hub with mirrors — the external system stays authoritative for its object |
| Sources per item | At most one remote per item (single-authority, always) |
| Core task state | Binary: todo / completed. No lifecycle enum, no attention enum |
| Deletion | The system never deletes tracked data; completed items form a permanent archive |
| Ontology | User-space: labels, projects, rules, saved views |
| Write path to remotes | Standard intents routed through connectors + durable offline outbox |
| Auth | Plugin-owned; the core is credential-agnostic |
| Query/expression language | CEL, everywhere |
| Proto management | buf, breaking-change checks on `v1` packages from day one |

## 2. Design principles

1. **Connectors are dumb; the core is smart.** A connector only translates
   ("list/get/watch objects in Linear, in Linear's terms"), resolves
   identity, and handles intents. Reconciliation, snapshot diffing, event
   generation, scheduling, and rules live in the core, written once. A new
   connector is ~200 lines of API glue.
2. **Facts and intent are separate layers with separate owners.** An item's
   *mirror* (facts about an external object) is connector-owned and
   downstream-only. An item's *todo* (my commitment) is user-owned and never
   touched by sync. Because ownership is partitioned, there is no field-level
   merge-conflict machinery anywhere in the system.
3. **Ontology lives in user space.** The schema knows todo/completed. What
   "urgent" means, when a PR becomes your problem, and when it stops being
   your problem are rules and labels you define. Plugins ship editable
   *templates*, never enforced semantics.
4. **The change feed is the universal integration point.** State + an
   append-only event log with cursors. Frontends staying live, rules firing,
   hooks, and agents all consume the same `Watch(since_cursor)` stream.
5. **Proto descriptors are the schema registry — and they are persisted.**
   Plugins register their extension types as `FileDescriptorSet`s; the core
   stores them permanently, so items remain renderable, filterable, and
   exportable even after their plugin is uninstalled.
6. **Frontend contributions are semantic and closed-vocabulary.** Plugins
   cannot express "a widget"; they express meaning (badge/tone, person,
   progress) and each frontend renders it natively. Degradation is
   automatic, not negotiated.
7. **Nothing destructive ever happens implicitly.** Sync never deletes,
   generic CRUD verbs never fan out to remote systems, and destructive
   remote effects exist only behind explicit, confirm-gated actions.

## 3. Architecture

```
                    ┌─────────────────────────────────────────┐
  CLI ──────gRPC──▶ │                 taskd                    │
  TUI ──────gRPC──▶ │  store (SQLite + event log + outbox)     │ ◀─gRPC── connector: linear
  web UI ─(gateway, │  sync engine        rules engine         │ ◀─gRPC── connector: github
   off by default)▶ │  intent router      schema registry      │ ◀─gRPC── connector: gcal
  MCP server ─────▶ │  contribution registry   secret store    │ ◀─gRPC── kind provider: …
                    │  plugin host (subprocess supervisor)     │ ◀─gRPC── hook: notifier
                    └─────────────────────────────────────────┘
                        plugins = supervised subprocesses
```

Data flows form a closed loop:

- **Mirrors flow one way:** remote → connector → core store. Nothing else
  writes mirror fields.
- **Intents flow the other way:** frontend/rule/agent → core intent router →
  connector → remote API → confirmed state → mirror.
- **Todos never leave the machine.** Sync cannot touch them.
- **Rules are the programmable wiring** between the two layers.

## 4. Data model

```protobuf
// taskcore/v1/item.proto
message Item {
  string id = 1;                    // ULID, assigned by core
  string kind = 2;                  // "task" (native), "linear.issue", "github.pr", ...
  Mirror mirror = 3;                // absent on native tasks
  Todo todo = 4;                    // absent on un-triaged mirrors
  repeated Relation relations = 5;  // PARENT_OF | INSTANCE_OF | BLOCKS | RELATES_TO
  uint64 mirror_revision = 6;       // optimistic concurrency, one counter per layer:
  uint64 todo_revision = 7;         //   sync bumps mirror_revision, user writes bump
                                    //   todo_revision — the layers can never falsely
                                    //   conflict with each other
  google.protobuf.Timestamp created_at = 8;
  google.protobuf.Timestamp updated_at = 9;
}

// Facts about the world. Connector-owned. Read-only to users and rules.
message Mirror {
  ExternalLink link = 1;            // exactly one authoritative remote
  string title = 2;
  string state = 3;                 // raw, kind-specific: "merged", "triggered", "running"
  map<string, google.protobuf.Any> data = 4;   // typed extensions (registered descriptors)
  bool stale = 5;                   // remote gone/unreachable; last-known snapshot kept
  google.protobuf.Timestamp missing_since = 6; // tombstone grace (snapshot sources)
}

// My intent. User-owned. Sync never writes here.
message Todo {
  bool completed = 1;
  google.protobuf.Timestamp completed_at = 2;
  string completed_reason = 3;      // optional free-form ("wontdo", "gone", ...)
  repeated string labels = 4;
  string project = 5;               // path-style, e.g. "work/reviews"
  google.protobuf.Timestamp due = 6;        // user override; see effective fields below
  google.protobuf.Timestamp snoozed_until = 7;
  string title_override = 8;        // display title defaults to mirror.title
  string note = 9;
}

message ExternalLink {
  string connector_instance = 1;    // "github@work" — instance, not just plugin
  string external_id = 2;
  string external_url = 3;
  string etag = 4;
  google.protobuf.Timestamp last_synced_at = 5;
}
```

**Item = mirror + todo:**

- Native task: `todo` only. Fully user-owned, fully editable.
- Un-triaged mirror: `mirror` only. Lands in the well-known `inbox` view
  until a rule or the user adds a todo.
- Tracked external item: both. The common case.

An item has **at most one remote**. Cross-references between remote objects
(a PR that fixes a Linear bug) are relations between two items, never two
links on one item — so every mirror always has exactly one authority.

### Facets and virtual fields

A **facet** is kind-level metadata, registered by kind providers: a
capability name plus CEL field bindings into that kind's data.

```
kind "calendar.event":  schedulable { due: mirror.data.event.start }
kind "linear.issue":    schedulable { due: mirror.data.issue.due_date }
                        assignable  { assignee: mirror.data.issue.assignee }
kind "task" (native):   schedulable { due: todo.due }
```

The core evaluates bindings to expose **universal virtual fields**: the
query `due < friday` works across native tasks, calendar events, and Linear
issues in one expression — including kinds installed tomorrow. Virtual
fields drive queries, columns, sorting, and standard intents.

**Effective values (override-wins):** for fields that exist in both layers,
the user's value wins when set, otherwise the mirror's bound value shows
through: `effective_due = todo.due ?? bound mirror due`. The connector
freely updates its side via sync; a user override is never overwritten;
clearing the override falls back to tracking the remote. Editing `due` can
optionally also push to the remote (per config) where the connector supports
the `set_due` intent. The same pattern extends to priority/assignee later.

### Labels, projects, views

User-owned and cheap: labels are strings with optional user-assigned
tone/icon; projects are path-strings; saved views are named `{filter (CEL),
sort, group_by, lanes, columns}` — the same schema whether user-defined or
plugin-contributed.

**Well-known view names** provide anchors without schema: `inbox`
(un-triaged mirrors), `today` (`effective_due <= today || snoozed_until <=
now`), `pending` (queued/failed outbox writes), `completed` (the archive).
`task ls` and every frontend show **active items only by default**;
the archive and inbox are explicit views.

## 5. State model: binary + never-delete

Core state is exactly: **does a todo exist, and is it completed.**

- Kind-specific state (`mirror.state == "merged"`) is data — displayable,
  filterable, meaningless to the core.
- "Needs my attention" is a label or saved view the user's rules maintain.
- "Won't do" / "stop tracking" is completion (`completed_reason: "wontdo"`).
  Because the item persists in the archive, the next sync updates its mirror
  quietly — there is no deleted-then-rediscovered zombie problem.
- "Waiting/blocked" is a label plus `snoozed_until`.

**The system never deletes tracked data.** Completed items form a permanent
archive with their external links intact (click through to the Linear issue
years later). When a *remote* object disappears, the item is kept, its
mirror is marked `stale` with the last-known snapshot frozen, and rules
decide what that means (auto-complete, label `gone`, nothing). Connectors
may also mark a link stale in response to `set_completed` where the remote
archives objects. `ItemService.Delete` exists for native items only and
never touches any remote. Archive volume is a non-issue at personal scale
(tens of thousands of rows/year); if it ever matters, archival to a side
table is a later optimization, not a design problem.

## 6. Tracking model: scope, triage, attach

**Scope.** Each connector instance has config (validated against the
plugin's config schema) including a scope filter: "PRs where I'm author or
reviewer", "mail under label `@follow-up`". Connectors mirror what's in
scope; nothing more.

**Triage is per-remote policy, chosen by the user.** The spectrum:

- **Automatic**: rules auto-add todos ("every PR requesting my review →
  project `reviews`"). Rules are per-instance config, so `github@work` can
  be automatic while `github@oss` is not.
- **Inbox**: mirrors arrive un-triaged; the user promotes them by hand.
- **Manual + enrich**: nothing is mirrored proactively. The user creates a
  task and *attaches* a remote object to it; the connector then keeps that
  one object fresh.

**Attach-to-remote.** `task link <item> <url>` → the connector's
`Resolve(url)` fetches the object and begins single-object tracking, even
outside its normal scope. This powers the "I just want this task to carry
live info about that Linear bug" workflow.

**Identity and enumeration.** Connectors own *translation and identity*:
they synthesize stable `external_id`s, using whatever source knowledge that
takes (content-hash + fuzzy path/text matching for code TODOs; the source's
stable instance ids and RRULE expansion for recurring calendar events —
expansion is translation of the source's domain model, which is exactly the
connector's job). The core owns *reconciliation*: connectors declare
`SNAPSHOT` (filesystem scan, EventKit) or `INCREMENTAL` (webhooks, cursors)
enumeration, and for snapshot sources the core diffs consecutive snapshots
keyed on `external_id` to synthesize created/updated/disappeared events. A
disappeared item gets `missing_since` (grace period) and then a stale mark —
never deletion. Recurring series are mirrored as a hidden series item with
materialized instance items (rolling horizon, connector config, default
±60d) linked `INSTANCE_OF`.

## 7. Rules engine

Rules bridge the mirror and todo layers. Declarative user config, CEL
conditions, evaluated by the core against the change feed. The store is the
source of truth (rules are edited via `RuleService` from any frontend);
files are an import/export format (`task rules export/import`).

```yaml
rules:
  - name: review-requests
    became: 'kind == "github.pr" && me in mirror.data.review_requests'
    do: { add: true, project: reviews, labels: { add: [code] } }

  - name: reviews-done
    became: 'kind == "github.pr" && (mirror.data.my_review_submitted || mirror.state in ["merged","closed"])'
    do: { complete: true }

  - name: ci-failures
    became: 'kind == "github.ci_run" && mirror.state == "failure"'
    do: { labels: { add: [failed, urgent] } }

  - name: morning-sweep                 # level semantics live in scheduled rules
    schedule: "0 8 * * *"
    where: '!todo.completed && effective_due < now'
    do: { labels: { add: [overdue] } }

  - name: linear-writeback              # the only write in the remote direction
    became: 'kind == "linear.issue" && todo.completed'
    do: { intent: set_completed }
```

**Execution semantics** (the part that makes automatic rules trustworthy):

- **Edge-triggered.** `became:` fires only when the condition flips from
  false to true, evaluated against the event's before/after images. A commit
  pushed to a PR whose review-request condition was already true does not
  re-fire the rule and cannot resurrect a completed todo. Doorbell, not
  "someone is at the door". Standing conditions use `schedule:` rules.
- **Provenance-guarded.** Every change records its cause (sync / client /
  rule X / intent confirmation). A rule never fires on changes it caused;
  chains are depth-bounded (~10) per originating event. Provenance doubles
  as the audit trail ("why did this todo appear?").
- **Deterministic and durable.** Rules run in config order, sequentially,
  each seeing the state the previous left. The engine tracks its own event
  cursor with at-least-once delivery; rule-dispatched intents carry dedup
  keys derived from (rule, item, cursor) so crash replays are safe.
- **Explicit label ops.** `labels: { add: [...], remove: [...] }` — no
  replace-vs-append ambiguity.
- **Not retroactive.** New or edited rules apply to future changes.
  `RuleService.DryRun` shows what would match; backfill against existing
  items is an explicit one-time action.

Plugins ship **rule and label templates**, copied into user config on
install, editable, never enforced.

## 8. The change feed

One append-only event log; `Watch(since_cursor)` streams it. Everything —
frontends, rules, hooks, agents — consumes the same feed.

```protobuf
message Event {
  uint64 cursor = 1;
  string item_id = 2;
  ChangeType type = 3;              // CREATED | UPDATED | DELETED (native only)
  Provenance caused_by = 4;         // SYNC(instance) | CLIENT(id) | RULE(name) | INTENT(id)
  repeated FieldChange changes = 5; // { path, old_value, new_value }
  Item item = 6;                    // full after-image
}
```

- **Old values ride in the event.** `state: "open" → "merged"` is inside the
  event itself — this is what makes "became merged" rules and edge detection
  implementable. A full before-image is reconstructable by reverting the
  changed fields on the after-image.
- **Echoes are silenced by value, not bookkeeping.** Incoming sync data is
  diffed against the stored mirror; a no-op (our own write echoing back)
  produces *no event*, so no rules fire. A genuine third-party change in the
  same window differs in value and fires correctly. (Outbox idempotency keys
  exist separately, solely to keep retried remote *creates* from
  duplicating.)
- **Retention + resync.** Events are kept ~30 days (configurable). A client
  resuming with an expired cursor gets `CURSOR_EXPIRED` and performs the one
  resync path — Query current state, resume from the snapshot's cursor —
  implemented once in the client SDK. Internal consumers (rules engine) hold
  compaction back; external clients get expiry.
- **Volatile fields are a second, ephemeral channel.** Extension fields
  marked volatile (live metrics, log tails) update in place: no revision
  bump, no event-log entries, no rule triggers; `Watch` sees them only by
  opt-in and they are not replayable. Scheduled rules may read them;
  `became:` rules cannot reference them. The durable feed keeps its
  resumability guarantee; the ephemeral channel honestly has none.

## 9. Write path: intents and the outbox

There are two kinds of fields, and they save differently:

- **Your fields** (note, labels, project, due, snooze): local, synchronous,
  instant.
- **Their fields** (a Linear issue's title): editing one means *asking the
  remote to change it*. The core translates the edit into a standard
  **intent**, dispatched to the connector.

**Standard intents**, anchored to declared facets; connectors declare which
they handle per kind:

```
(any mirror)   rename, add_comment, delete
schedulable    set_due, set_start
assignable     assign
prioritized    set_priority
completable    set_completed        ← rules' write-back uses this too
```

**One write API, one-layer calls.** `ItemService.Update` routes: native or
todo fields → local commit; mirror-backed fields → intent (rejected as
read-only if the connector doesn't handle it). A single Update may touch
only one layer — mixed calls are rejected so partial-success semantics never
exist; frontends issue two calls invisibly.

**The outbox.** Intent dispatch returns an `IntentTicket`: the core waits
briefly (~2s) and returns confirmed state when the connector is responsive;
otherwise `QUEUED`. Offline or remote-down, intents wait in a durable queue;
the item shows a **pending** badge; the queue drains on reconnect. Terminal
failures ("issue was deleted") flip the badge to **failed** with the reason,
emit an event (rules/notifier can react), and park until retried or
discarded. The well-known `pending` view lists everything queued or failed —
nothing dies silently. Transient errors retry with backoff automatically.

**One dispatch path for everything.** User edits, rule write-backs, and
agent writes all flow through the intent router: one place for permissions,
provenance, and audit.

**Invariant to protect in implementation:** the mirror only ever holds
remote-confirmed truth. Optimistic *display* is fine; optimistic mirror
*writes* are not — that would silently reintroduce the conflict engine this
design deleted.

Custom actions (`github.merge_pr`, `pagerduty.escalate`) coexist unchanged;
standard intents are the well-known subset enabling a uniform editing
surface. Destructive remote effects (remote delete, "remove TODO from source
code") are custom confirm-gated actions only — never reachable via generic
CRUD or sync.

## 10. Plugin system

**Mechanics:** hashicorp/go-plugin model. A plugin is any executable serving
gRPC over a local unix socket, launched and supervised by `taskd` (crash →
restart with backoff; a misbehaving plugin cannot take down the core).
Handshake returns a manifest: name/version, implemented services, descriptor
sets, config schema, per-kind facet bindings and intent capabilities,
contribution set, rule/label templates, and the authenticated principal per
instance (below).

**Capability services** (a plugin implements any subset):

- `Connector` — `Capabilities`, `List(cursor)`, `Get`, `WatchRemote`,
  `Resolve(url)` (attach-to-remote), `HandleIntent`, custom actions. Maps
  external-speak → `Item`s; zero sync logic.
- `KindProvider` — registers kinds, facet bindings, descriptor sets,
  validation.
- `Hook` — receives the change feed with a CEL server-side filter.
  (User-facing automation belongs in rules; hooks are for extensions that
  need code, e.g. a notifier.)
- `ViewContributor` — declarative UI contributions (§11) + `ResolveOptions`
  for dynamic option lists.

**Connector instances.** `github@work` and `github@oss` are two instances of
one plugin, each with its own config and scope. Instance name namespaces
external links.

**Auth is plugin-owned; the core is credential-agnostic.** Each plugin runs
its own OAuth/token flow ("connect your account" is an ordinary plugin
action that opens the browser). The core offers one *optional*, auth-agnostic
helper: a **secret store** — put/get opaque blobs, backed by the macOS
keychain (or libsecret) — so plugins don't each invent token storage and
none write plaintext files. The core never parses or understands what it
stores.

**Identity (`me`).** Each connector instance reports its authenticated
principal (id, handle, email) at handshake via a whoami call. CEL binds `me`
per-instance during evaluation: `me in mirror.data.review_requests` means
the GitHub login on `github@work` items and the Linear UUID on Linear items,
with zero user configuration.

**Descriptors outlive plugins.** Registered descriptor sets and contribution
sets are persisted in the store, versioned per plugin version. Uninstall the
Linear plugin and your archived `linear.issue` items stay renderable,
filterable, and exportable (contributions go dormant: render-only, no
actions). At handshake the core runs a buf-style breaking-change check of
new descriptors against stored ones and **refuses to load a plugin whose
schema would make stored data unreadable** — additive-only evolution,
enforced mechanically; a genuinely new shape means a new kind name.

**Rejected:** in-process Go plugins (fragile, Go-only), WASM sandboxing
(revisit only if untrusted third-party plugins become a goal).

## 11. Frontend contribution model

The N-plugins × M-frontends problem is solved by making both sides target a
contribution spec in the core. **Frontends never talk to plugins**; the core
merges, validates, and serves contributions, and proxies dynamic bits.

**Frontend archetypes** (the spec must serve all of them): one-shot text
(CLI), persistent terminal (TUI), graphical (web/desktop), agent (MCP —
schemas, no visuals), notifier (a summary line + urgency + deep link).

**Closed display vocabulary.** Every contribution expression produces a
`DisplayValue` from a small semantic set:

```
text · badge{label, tone} · datetime{relative?} · duration · person{name, avatar?}
link{label, url} · progress{done, total} · icon(symbolic) · count
```

Tones are semantic (`neutral|info|success|warning|danger|accent`), never
colors; icons are symbolic names. A plugin *cannot express* a contribution
that only works in one frontend — degradation is automatic (web: red chip;
TUI: ANSI `[Urgent]`; CLI: plain text; notifier: urgency; MCP: text).

**Five contribution types:**

1. **Presentation** per kind — summary (icon, title/subtitle exprs, badges),
   columns `{id, label, expr, sortable, width_hint}`, detail sections
   (labeled rows + special sections: checklist, related-items query, links).
2. **Forms** — no new schema language: field types/validation come from the
   registered proto descriptor; a `FormHints` overlay adds order, groups,
   labels, help, widget hints, CEL `visible_when`/`required_when`, and
   dynamic options via core-proxied `ResolveOptions`.
3. **Actions** — `{name, title, icon, applicability (CEL), params descriptor
   + FormHints, confirm, scope: item|bulk|global}`; invoke is
   server-streaming for progress.
4. **Views** — filter/sort/group/lanes; lanes let one definition render as a
   CLI list, TUI board, and web kanban.
5. **Nav nodes** — optional tree entries; frontends without navigation
   ignore them.

**Core-side rendering.** Frontends don't embed CEL. `Query`/`Watch` accept a
`RenderSpec` and return display rows alongside raw items; the core evaluates
contribution expressions server-side. Frontends are thin: map DisplayValues
to their medium. (Go frontends *may* evaluate locally; nothing requires it.)

**MCP falls out for free.** Descriptor + FormHints → MCP tool input schemas;
applicability CEL → which item-actions an agent is offered; views → MCP
resources; intent capabilities → what `update_item` accepts per item.
Installing a plugin makes agents strictly more capable with zero MCP-server
changes.

**Escape hatch, with a rule.** A contribution may include `web_embed {url,
placement}` (plugin-served web component, e.g. experiment loss curves).
Embeds may only *augment*: the declarative fallback fields are `required` in
the schema, so terminal frontends and agents always have something real.

**Plugin-author DX:** `task plugin lint` (expressions typecheck against
registered descriptors, icons exist, embeds have fallbacks) and
`task plugin preview` (renders a contribution as list row / detail pane /
`task ls` line / MCP tool JSON). The preview renderer doubles as the
conformance fixture for frontend implementers.

## 12. API surface

**Core services** (implemented by `taskd`, for clients):

- `taskcore.v1.ItemService` — Create/Get/Update/Delete/Query/Watch. Query
  takes CEL + optional RenderSpec, paginates (page_size/page_token); Watch
  streams §8 events from a resumable cursor. Update routes per §9. `Link`
  attaches a remote object to an item.
- `taskcore.v1.SchemaService` — kinds, facet bindings, descriptors, intent
  capabilities per kind.
- `taskcore.v1.ViewService` — merged contributions per frontend archetype,
  WatchContributions, proxied ResolveOptions.
- `taskcore.v1.ActionService` — discover/invoke actions and intents; list
  pending/failed intents.
- `taskcore.v1.RuleService` — CRUD rules, DryRun, backfill, accept plugin
  templates.
- `taskcore.v1.ConfigService` — plugin/instance config.

**Plugin-facing services** (implemented by `taskd`, for plugins — a
*separate, least-privilege surface*, see §14): mirror upsert scoped to the
plugin's own instances, secret store, event subscription for hooks.

**Plugin services** (implemented by plugins): `taskcore.plugin.v1.Connector`,
`.KindProvider`, `.Hook`, `.ViewContributor` — treated as sacred; buf
breaking-change checks from day one.

**Everything else is a client.** CLI (`task`) and MCP server (`task-mcp`)
are thin gRPC clients; the web UI connects via a gateway (§14); the TUI
speaks gRPC directly.

## 13. Storage & query

- **SQLite, WAL mode, on disk.** Items as serialized proto blobs + extracted
  indexed columns (kind, completed, project, effective due, updated_at) +
  JSON projection for ad-hoc pushdown; event log and outbox as tables; FTS
  for free-text search. Behind a `Store` interface.
- **CEL everywhere:** query filters, rule conditions, view definitions,
  action applicability, contribution expressions, facet bindings, hook
  subscriptions. One expression language, proto-native, including reflected
  extension fields and virtual fields.
- **Scale envelope:** personal scale (~100k items, ~1M events, ~10
  watchers) — SQLite is comfortably sufficient. Active-only defaults keep
  working sets small; predicates beyond the indexed virtual-field columns
  scan, which is acceptable at this envelope and stated here so nobody
  "fixes" it prematurely.

## 14. Security

Threat model for a localhost, single-user app: not network attackers —
**other local processes, malicious web pages, and the plugins themselves.**

1. **Unix sockets with owner-only permissions (0600), never localhost TCP**,
   for both the client API and plugin channels. Localhost TCP is reachable
   by every process of every user on the machine; a socket with file
   permissions is reachable only by you. gRPC itself is not a security
   boundary; the socket permissions are.
2. **Web gateway hardened against the browser**: off by default. When
   enabled: loopback bind, per-session bearer token carried in a header (not
   a cookie — kills CSRF), and Host-header validation (kills DNS rebinding,
   the classic way a malicious webpage reaches localhost services).
3. **Least-privilege plugin surface**: plugins talk to a dedicated core API,
   not the client API. A connector can upsert mirrors only under its own
   instance namespace; it cannot read todos, other connectors' items, or
   secrets it didn't store. Honest statement: plugins are trusted code
   running as the user — v1 has no sandbox; the mitigation is API scoping,
   not containment.
4. **Agents**: MCP callers are a distinct principal class. Confirm-gated
   and destructive actions are refused for agents by default (an agent has
   no confirm dialog); the user may grant a per-action allowlist in config.
5. **Audit**: every write carries provenance (§8) with actor identity;
   persisted. Free audit log.
6. **Secrets** live in the OS keychain via the core secret store (or the
   plugin's own choice — the store exists so the easy path is the safe one).

## 15. Operations

- **Backup.** The SQLite file is the canonical store — and todos exist
  *only* on this machine, so backup is non-optional. `task backup` uses
  SQLite's online-backup API (safe against a live WAL database, where a raw
  `cp` mid-write can corrupt); cron-able. `task export` emits a documented
  JSONL format (items + rules + config) for portability and paranoia;
  `task import` restores it.
- **Daemon lifecycle.** Flexible, nothing forced: `task daemon run` in the
  foreground; shipped launchd/systemd templates for auto-start; the CLI
  dials the socket and offers to spawn the daemon if absent. CLI↔daemon
  version handshake warns on skew (buf keeps the wire compatible).
- **Multi-device is out of scope for v1**, explicitly, so it cannot quietly
  warp the architecture. The `Store` interface is the seam if that ever
  changes.

## 16. Testing

The subtle machinery is exactly the core-owned parts, so the harnesses are
design-level requirements (they shape interfaces — injectable clock,
deterministic IDs and cursors — which must exist from the first commit):

- **`connectortest`** (in the plugin SDK): a scripted fake remote — "object
  appears, changes, disappears, comes back" scenarios — asserting mirror
  convergence, snapshot diffing, tombstone grace, and echo silence. Doubles
  as the conformance suite every connector runs in CI.
- **Rules replay**: given an event-log fixture and a ruleset, assert the
  exact actions taken (golden files). Edge semantics and provenance guards
  live or die here.
- **Outbox chaos**: kill/restart the daemon between enqueue, dispatch, and
  confirmation; assert exactly-once remote effects via dedup keys.

## 17. Repo layout

```
proto/taskcore/v1/…             # item, events, services   ─┐ buf module —
proto/taskcore/plugin/v1/…      # plugin services            ─┘ the real public API
proto/taskcore/view/v1/…        # contributions, DisplayValue
cmd/taskd  cmd/task  cmd/task-mcp
internal/store  internal/sync  internal/rules  internal/intent
internal/schema  internal/plugin  internal/secret
pkg/taskplugin                  # public Go SDK for plugin authors (+ connectortest)
plugins/linear  plugins/github  plugins/gcal  …   # independent binaries
```

## 18. Build order

1. **Walking skeleton:** protos + store + ItemService/Watch (events per §8
   from day one) + CLI. A working native-task app: binary state, labels,
   projects, views, active-only `task ls`.
2. **Plugin host + one read-only connector** (calendar: exercises mirroring,
   snapshot diffing, recurrence/relations, and the mirror/todo split without
   write risk).
3. **Rules engine** — edge semantics, provenance, scheduled rules, triage/
   inbox flow, templates, DryRun.
4. **Intents + outbox** + Linear connector (bidirectional) + attach-to-remote.
5. **MCP server + contributions** (actions, forms, views → tools/resources;
   agent guardrails).
6. **TUI / web frontends** against the contribution spec; web gateway
   hardening lands with the web frontend.

## 19. Alternatives considered

- **Lifecycle/state/attention enums in core** — a universal ontology in the
  schema that every integration argues with forever ("is a draft PR
  active?"). Ontology moved to user space instead; the schema keeps only
  what is universal for a personal tracker.
- **Multi-source items / cross-app replication** — one item mirrored in two
  remotes reintroduces "which remote wins" and with it a conflict engine.
  This app centralizes; it does not broker. One remote per item, always.
- **Field-level bidirectional sync with three-way merge** — the mirror/todo
  ownership split plus intent routing deletes the entire conflict engine.
  Guard the invariant: mirrors hold only remote-confirmed truth.
- **Deletion + retention machinery** — deleting tracked data risks user
  intent (notes, labels) and creates rediscovery zombies; never-delete with
  a permanent archive is simpler and safer, and personal-scale volume makes
  it free.
- **Core-brokered auth** — would make the core opinionated about every
  provider's flow. Plugins own their auth; the core offers only an opaque,
  keychain-backed secret store.
- **Sync logic in connectors** — every new source would get harder instead
  of easier. Connectors own translation and identity; the core owns
  reconciliation and policy.
- **Per-frontend plugin UI code** — an N×M explosion. Closed semantic
  vocabulary + core-side rendering instead.
- **In-process plugins / WASM** — fragility / a detour; subprocess gRPC
  keeps plugins language-agnostic and crash-isolated.
