# Design: a small task backend with many frontends

*Status: current — 2026-07-04. Supersedes the previous plugin-platform
design in full.*

## 1. What this is

`taskd` is a Go daemon that stores tasks in SQLite and serves one
gRPC/protobuf API. Everything else is a client of that API:

- the **CLI** (`task`) and, later, a TUI — Go;
- a **web frontend** (Todoist-like) — TypeScript, later;
- an **MCP server** (`task-mcp`) so agents can read and create tasks;
- **syncers** — small programs that mirror external task-like things
  (calendar events, PRs, tickets) into tasks.

The product is a personal tracker. Tasks are classified by **labels** and an
optional **due date**. That's the whole ontology.

## 2. What this deliberately is not

The previous design was an extension *platform*: subprocess plugins with
manifest registration, runtime schema/kind registries, CEL as a query and
rules language, a durable write-back outbox, server-side rendering of
plugin-contributed UI, and an event log with cursors and retention. All of
it existed to let *unknown third parties* extend a frozen core.

There are no unknown third parties. Every consumer is first-party, lives in
(or next to) this repo, and is released together with the daemon. So the
platform machinery was cut — roughly half the codebase — and each piece got
a one-sentence replacement:

| Was | Is |
|---|---|
| Plugin protocol (manifests, descriptor sets, schema gates) | **The public API is the extension API.** An extension's syncer is just a client; the daemon supervises the process and serves its UI bundle, nothing more (§5a). |
| Two-layer Item (Mirror + Todo, dual revisions) | One `Task` + a field-ownership convention (§5). |
| Kinds, facets, virtual fields, typed extension schemas | Labels, plus opaque `external_data` / `user_data` blobs the extension owns. |
| CEL filters + SQL pushdown + residual evaluation | A structured `TaskFilter` message; every dimension is indexable. |
| Rules engine | Doesn't exist. A "rule" is a small client watching for changes, if ever needed. |
| Intents + durable outbox + idempotency keys | Doesn't exist. Syncing is one-way per field, so there is nothing to write back (§5). |
| Event log, cursors, retention, resync protocol | A dumb live stream: reconnect ⇒ refetch (§6). |
| Server-side rendering of UI contributions | Frontends render; extensions ship JS bundles loaded at runtime (§5a). |

The guiding rule (rule of three): an abstraction is added when the second
or third *concrete* consumer shows up, not speculatively. The extension
system itself earned its place this way — the calendar and bug integrations
were the concrete second and third consumers that a plain "syncer is a
client" note couldn't serve (they needed real UI), so the mechanism grew to
exactly fit them and no further.

## 3. API stance: unversioned, frozen-when-stable

The proto package is `task` — no `v1`, and there will never be a `v2`. The
plan:

1. While the system is young the API changes freely; all clients are in
   this repo and recompile together.
2. Once it settles, the API is **frozen**: from then on it evolves only
   additively — new fields, new RPCs, new filter dimensions. Nothing is
   renamed, renumbered, or removed. Proto3 semantics (unknown fields are
   ignored, absent fields are defaults) make additive changes invisible to
   old clients.

This is the entire versioning story. No capability negotiation, no feature
flags, no must-ignore registries: protobuf already provides the mechanics,
and first-party clients provide the discipline.

## 4. The data model

One message. See `proto/task/task.proto` for the authoritative,
fully-commented contract.

```
Task {
  id, title, notes,
  labels[],            // flat strings; "p1", "project:home", "agent:claude"
  due_time?,           // the one scheduled-time field
  completed_time?,     // unset = active; set = done
  source,              // "" = local; else the syncer that owns it
  external_ref,        // the task's identity in the source system
  external_data,       // Struct; source-owned detail, display-only
  user_data,           // Struct; user/client-owned extension data
  revision,            // optimistic concurrency + watch ordering
  create_time, update_time
}
```

Decisions:

- **Labels subsume priority, project, and context.** `p1` and
  `project:home` are conventions frontends can render specially (a
  `project:` prefix becomes a project picker), but the backend stores flat
  strings. New classification schemes cost nothing.
- **Completion is a timestamp, not a bool** — free "completed this week"
  queries, and unset naturally means active.
- **Two opaque extension blobs, split by owner.** `external_data` is
  source-owned (a syncer writes it every sync); `user_data` is
  user/client-owned (sync never touches it). Both are stored and returned
  verbatim, never interpreted or filtered on by the server; extensions
  namespace their keys (`user_data.timebox`, `user_data.gcal`, …) so they
  coexist without a registry. If a filter dimension is ever needed on
  external detail, the syncer *lifts it into a label* (e.g. `pr:approved`).
- **No task hierarchy, no recurrence in the schema.** Sub-tasks, recurring
  tasks, and reminders are real features, but each is an additive field or
  RPC later — none justifies pre-building now.

## 5. Sync: one-way field ownership instead of conflict resolution

External task-like things arrive through one RPC:
`UpsertExternalTasks(source, batch, apply_labels, full_snapshot)` —
reconcile a source's tasks, keyed by `(source, external_ref)`.

Per synced task, ownership is split by *field*, one writer each:

- the **source** owns `title`, `due_time`, `completed_time`,
  `external_data` — every upsert overwrites them;
- the **user** owns `labels`, `notes`, and `user_data` — upserts never
  touch them (`apply_labels` only ever adds).

Because no field has two writers, there is no conflict engine, no mirror
layer, and no write-back path. Completing a synced task locally is allowed
and meaningful ("I'm done with this"), but the next sync restores the
source's view of `completed_time` — the source is authoritative for its own
fields. A syncer that wants local completion to close the remote PR can
watch for it and call the remote API itself; that logic belongs in the
syncer, not the core.

## 5a. Extensions: the daemon stays dumb

Integrations and their UI are **extensions** — folders under
`~/.taskd/extensions/`, outside the main codebase, that users can install
without touching (or rebuilding) the core. An extension has up to two
halves: a *syncer* process (the daemon-half) and a *web bundle* (the
frontend-half). The daemon's entire involvement is mechanical: supervise the
syncer process (with `TASKD_ADDR` injected) and serve the `web/` folder at
`/ext/<name>/`. It never learns what an extension means — no plugin
protocol, no schema registration, no typed capabilities. This is the lesson
from the discarded platform design (§2): make the core dumb about
extensions and push the intelligence to the edges, where it can live
out-of-tree.

Two consequences make this safe and open-ended:

- **A syncer has no privileged access.** It reaches the daemon only through
  the public API — exactly what any third-party program could do. The
  in-tree `extensions/ics` is both the shipped calendar integration and the
  template to copy. (This is why config-file syncers were removed and ICS
  moved out of core: if the extension path isn't good enough for our own
  integration, it isn't good enough.)
- **The frontend half gets real code, not config.** A calendar view with
  drag-to-timebox can't be declarative. Extensions ship JS bundles loaded at
  runtime against a small typed API (`web/extension-api`) — presenters that
  customize how their tasks render, and whole contributed views — sharing
  the host's React instance. `user_data` is the store behind
  client-authored structured data like the timebox.

Installing an extension is installing software (arbitrary code, both
halves) — the right trade for personal tooling, and the same one Obsidian or
a shell's plugins make. Sandboxing would cost more than the whole system.

## 6. Live updates: refetch, don't replay

`WatchTasks` streams changes from the moment the stream opens. No history,
no cursors, no retention. A client builds a consistent live view by:
subscribe → list → apply events, using `revision` to drop events older than
the snapshot. Stream dropped (or the server dropped you as a slow
consumer)? Reconnect and refetch.

At personal scale a full refetch is milliseconds; an event-log replay
protocol saves nothing and costs a subsystem.

## 7. Transport: Connect RPC

The daemon serves **Connect, gRPC, and gRPC-Web on one port** (h2c). This
is what makes "many frontends" cheap:

- Go clients (CLI/TUI/MCP/syncers) use the generated Connect client;
- the TypeScript web app uses `connect-es` generated from the same protos,
  no gateway or proxy;
- `curl` works for debugging (Connect's JSON encoding).

Health (`GET /healthz`) is transport-level, not part of the API. The daemon
binds localhost TCP by default (plus an optional 0600 unix socket). Nothing
in the architecture changes if it is later hosted remotely — add auth as an
interceptor and TLS at the edge.

## 8. Storage

SQLite (`modernc.org/sqlite`, pure Go), WAL mode. Tasks are stored in
columns (no proto blobs), labels in a `task_labels` join table, timestamps
as unix milliseconds. Every filter dimension maps to an index or a join.
Pagination is keyset-based (opaque token), never OFFSET. Text search is
case-insensitive substring over title+notes; FTS5 is the designated upgrade
if it ever feels slow.

## 9. Invariants

1. Every mutation bumps `revision` by exactly 1; watchers can order
   evidence by it.
2. `(source, external_ref)` is unique among synced tasks; local tasks have
   both empty.
3. Only `UpsertExternalTasks` creates or rewrites source-owned fields of
   synced tasks; only user RPCs touch `labels`/`notes`/`user_data`.
4. `full_snapshot` pruning deletes only rows of the named source; local
   tasks are untouchable by syncers.
5. Labels returned by the API are always trimmed, deduplicated, and
   sorted; matching is exact and case-sensitive.
6. The store never interprets `external_data` or `user_data`.
7. Watch events carry the full new state (or the deleted id) — a client
   never needs history to converge.
8. The daemon serves only each extension's `web/` folder; syncer binaries,
   sources, and configs are never exposed over HTTP.
