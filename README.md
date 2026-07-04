# taskd

A small task backend with many frontends. One Go daemon owns a SQLite store
and serves one Connect/gRPC API; the CLI, the MCP server (for agents), the
future web UI and TUI, and every integration are all just clients of that
API. Tasks are classified by **labels** (priorities and projects are label
conventions, not schema) and an optional **due date**; external task-like
things — calendar events, PRs, tickets — sync in as tasks with a `source`
and display-only `external_data`.

See [DESIGN.md](DESIGN.md) for the rationale and
[ARCHITECTURE.md](ARCHITECTURE.md) for the map of what exists.

## Quick start

```sh
make build       # builds ./taskd, ./task, ./task-mcp
./taskd &        # serves http://127.0.0.1:7517, data in ~/.taskd
./task add "write the calendar connector" -l dev -l project:taskd --due tomorrow
./task ls
./task done <id-prefix>
./task watch     # live change stream
```

Agents connect through MCP:

```sh
./task-mcp       # stdio MCP server; tools: list/create/update/complete tasks
```

Calendars (and other sources) sync in via the daemon's `~/.taskd/config.yaml`:

```yaml
syncers:
  - type: ics
    name: work                       # tasks arrive with source "ics:work"
    url: https://example.com/cal.ics
    interval: 15m
    labels: [calendar]
```

A syncer is not a plugin — it is an ordinary API client that calls
`UpsertExternalTasks`. Anything the built-in ICS syncer can do, an external
program in any language can do identically. That is the entire
extensibility model.

## Layout

```
proto/task/task.proto  the public API — one service, fully commented; read this first
gen/                   generated Go (committed; `make generate` to refresh)
internal/store/        SQLite: filters→SQL, keyset pagination, sync upsert
internal/server/       TaskService handlers + watch fan-out
internal/daemon/       assembly: config, listeners, syncers
internal/syncer/       sync loop + built-in syncers (ics)
pkg/client/            dialing helper every Go client uses
cmd/taskd, cmd/task, cmd/task-mcp
```

## Development

```sh
make lint       # buf lint + gofmt + go vet
make test       # go test -race ./...
make generate   # after proto changes (needs buf, protoc-gen-go, protoc-gen-connect-go)
```

Conventions that matter:

- **The proto is the contract** and is deliberately unversioned: it changes
  freely until it settles, then freezes and only grows additively
  (DESIGN.md §3).
- **Field ownership replaces sync machinery.** On synced tasks the source
  owns `title/due/completed/external_data`; the user owns `labels/notes`.
  No code may cross that line (DESIGN.md §5).
- **Only the store touches SQL; everything else speaks the API.** Syncers
  included.
