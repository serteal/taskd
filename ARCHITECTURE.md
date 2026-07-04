# Architecture

How the code is laid out, what the public API is, and how to build against
it. The design rationale lives in [DESIGN.md](DESIGN.md); this is the map of
what exists.

## The one-sentence architecture

A Go daemon (`taskd`) owns a SQLite store and serves one Connect/gRPC
service (`task.TaskService`); the CLI, the MCP server, the web frontend,
and every integration are all just clients of that service.

## Layout

```
proto/task/task.proto     The entire public API, fully commented. Read this first.
gen/task/                 Generated Go (protoc-gen-go + protoc-gen-connect-go).
internal/store/           SQLite: schema, filters→SQL, keyset pagination, upsert.
internal/server/          TaskService handlers + the watch fan-out hub.
internal/daemon/          Assembly: config, listeners, syncer startup.
internal/syncer/          Syncer loop + built-in syncers (ics).
pkg/client/               Dials a taskd; the one place target resolution lives.
cmd/taskd/                The daemon.
cmd/task/                 The CLI.
cmd/task-mcp/             MCP server for agents.
```

Dependency direction: `cmd/* → pkg/client → gen/task` and
`cmd/taskd → internal/daemon → internal/{server,store,syncer}`. Nothing
outside `internal/store` touches SQL; nothing outside `internal/server`
touches the store; syncers and all binaries speak only the public API.

## Runtime

`taskd` listens on `127.0.0.1:7517` (config `listen:`) and optionally a
0600 unix socket (config `socket:`). One h2c port serves the Connect,
gRPC, and gRPC-Web protocols simultaneously, plus `GET /healthz`. Data
lives in `$TASKD_DIR` (default `~/.taskd`): `tasks.db` and `config.yaml`.

```yaml
# ~/.taskd/config.yaml — everything optional
listen: 127.0.0.1:7517
socket: /Users/me/.taskd/taskd.sock
syncers:
  - type: ics
    name: work            # task source becomes "ics:work"
    url: https://example.com/cal.ics
    interval: 15m
    labels: [calendar]
```

Clients resolve the daemon address from `TASKD_ADDR`
(`http://host:port` or `unix:///path`), defaulting to
`http://127.0.0.1:7517`.

## The API

`proto/task/task.proto` is the authority — every RPC and field is
documented there. The shape:

| RPC | Purpose |
|---|---|
| `CreateTask` | New local task (title, notes, labels, due). |
| `GetTask` / `DeleteTask` | By id. Delete is permanent. |
| `UpdateTask` | Field-mask write over `title, notes, labels, due_time, completed_time`; optional `expected_revision` (mismatch ⇒ `ABORTED`). Completing = setting `completed_time`. |
| `ListTasks` | Structured `TaskFilter` + `order_by` (`created`/`updated`/`due`/`title`, ` asc`/` desc`) + keyset pagination. |
| `UpsertExternalTasks` | Syncer entry point: reconcile one source's tasks in a batch keyed `(source, external_ref)`. |
| `WatchTasks` | Server stream of every change from now; no history. |
| `ListLabels` | Distinct labels + counts, for filter chips. |

Error codes: `NOT_FOUND`, `ABORTED` (revision), `INVALID_ARGUMENT`
(validation, bad order_by/page_token), `RESOURCE_EXHAUSTED` (watcher fell
behind — reconnect and refetch).

The live-view protocol: open `WatchTasks` and await the first message — an
**empty handshake** confirming the subscription is live — then `ListTasks`,
then apply events, using `revision` to discard events older than the
snapshot. Ignore any watch message whose `change` is unset or unrecognized
(the handshake today; new change kinds tomorrow). Any stream drop ⇒
reconnect and refetch. There is no cursor to manage.

## Writing a client

**Go** — use `pkg/client`:

```go
tc := client.New(client.Target())
res, err := tc.CreateTask(ctx, connect.NewRequest(&taskpb.CreateTaskRequest{
    Title:  "Ship the demo",
    Labels: []string{"p1", "project:launch"},
}))
```

**TypeScript (web)** — generate from the same protos with
[`connect-es`](https://connectrpc.com/docs/web/getting-started) and point a
transport at the daemon URL. The daemon already speaks gRPC-Web on its one
port; no proxy, no gateway, no REST shim.

**curl** — Connect's JSON encoding works everywhere:

```sh
curl -s http://127.0.0.1:7517/task.TaskService/ListTasks \
  -H 'content-type: application/json' \
  -d '{"filter": {"labelsAll": ["p1"], "completed": false}}'
```

Client rules (the compatibility contract, enforced by convention until the
freeze):

1. Ignore fields you don't recognize; never fail on them.
2. Treat labels as opaque strings. Prefix conventions (`project:`, `p1`,
   `agent:`) are UI sugar, resolved at render time.
3. Send `expected_revision` when editing something a user has been staring
   at; handle `ABORTED` by refetch-and-retry or asking the user.
4. Recover watch streams by refetching, not by resuming.

## Writing a syncer

A syncer mirrors an external system into tasks. It is an ordinary API
client — the built-in ones run inside the daemon only for convenience and
use zero private hooks. The loop:

1. Fetch your source's state (a calendar, your review queue, a tracker).
2. Convert each item to an `ExternalTask{external_ref, title, due_time,
   completed_time, external_data}`.
3. `UpsertExternalTasks(source, batch, apply_labels, full_snapshot)`.

That's the whole contract. The server handles create/update/unchanged
detection and — with `full_snapshot` — pruning of items that vanished from
the source. Field ownership does the rest: your batch owns
title/due/completed/external_data; the user's labels and notes survive
every sync. Put anything you want frontends to *display* in
`external_data`; lift anything you want users to *filter on* into a label
via `apply_labels` (or per-task refs → separate sources).

Built-in syncers implement `internal/syncer.Syncer` and register in
`builders`; out-of-process syncers just link `pkg/client` (or any gRPC
stack in any language) and run on their own schedule.

## Storage

Column-mapped SQLite, WAL, single writer. Timestamps are unix
milliseconds; proto timestamps are truncated to ms on write.

```
tasks(id PK, title, notes, due_ms?, completed_ms?, source, external_ref,
      external_data JSON, revision, created_ms, updated_ms)
task_labels(task_id → tasks ON DELETE CASCADE, label; PK(task_id, label))
```

Unique partial index on `(source, external_ref)` where source ≠ ''.
Every `TaskFilter` dimension maps to an index or a `task_labels` join; text
search is `LIKE` over title+notes (FTS5 is the designated upgrade).
Pagination is keyset (`ORDER BY sortkey, id` + comparison against the
token's last-row keys); tokens embed the order and are rejected on
mismatch. `OFFSET` does not appear in the codebase.

## Invariants

See DESIGN.md §9. The load-bearing ones for contributors: revision bumps by
exactly 1 per write; only upserts write source-owned fields and only user
RPCs write labels/notes; `full_snapshot` pruning cannot touch local tasks;
the store never interprets `external_data`; watch events carry full state.

## Development

```sh
make generate   # buf generate (protoc-gen-go, protoc-gen-connect-go)
make test       # go test -race ./...
make lint       # buf lint, gofmt, go vet
make build      # taskd, task, task-mcp
```

Proto changes: edit `proto/task/task.proto`, `make generate`, fix
compile errors. Pre-freeze that's the whole process; post-freeze, changes
must be additive (see DESIGN.md §3).
