# taskd

A small task backend with many frontends. One Go daemon owns a SQLite store
and serves one Connect/gRPC API; the CLI, the MCP server (for agents), the
web app, and every integration are all just clients of that API (a TUI is
still to come). Tasks are classified by **labels** (priorities and projects
are label conventions, not schema) and an optional **due date**; external
task-like things — calendar events, PRs, tickets — sync in as tasks with a
`source` and display-only `external_data`.

See [DESIGN.md](DESIGN.md) for the rationale and
[ARCHITECTURE.md](ARCHITECTURE.md) for the map of what exists.

## Quick start

```sh
make build-web   # builds ./taskd (web UI embedded), ./task, ./task-mcp
./taskd &        # serves http://127.0.0.1:7517, data in ~/.taskd
open http://127.0.0.1:7517   # the web app — same port as the API
./task add "write the calendar connector" -l dev -l project:taskd --due tomorrow
./task ls
./task done <id-prefix>
./task watch     # live change stream
```

The web app is a live replica: change a task from the CLI, a syncer, or
another window and it appears in the browser instantly (rows that changed
from outside pulse once). It's a Todoist/Linear-flavored app —
list and board views, a quick-add overlay, a ⌘K command palette + search,
multi-select and bulk actions, undo, drag-to-timebox onto a day-timeline
rail, saved views, and a hand-authored SVG icon set — and keyboard-first
(`q` add, `⌘K` commands, `j/k` move, `x` done, `?` for the full list).
(`make build` skips the UI and Node entirely; taskd then serves the API
plus a pointer page.)

Agents connect through MCP:

```sh
./task-mcp       # stdio MCP server; tools: list/create/update/complete tasks
```

## Extensions

External systems — calendars, GitHub issues, anything task-like — sync in
through **extensions**: folders in `~/.taskd/extensions/<name>/` that the
daemon discovers. An extension has up to two halves, both optional:

- a **syncer** (any-language binary) that mirrors a source into tasks via
  the public `UpsertExternalTasks` RPC — it's an ordinary API client, not a
  plugin with special access;
- a **web bundle** the app loads at runtime to present those tasks (row
  badges, a detail section) or add a whole view (e.g. a calendar).

The daemon supervises the syncer and serves the bundle; it never learns what
an extension *means*. Nothing in the core changes to add one — you drop in a
folder. In-tree examples: `extensions/ics` (calendar feeds, real),
`extensions/gcal` (calendar + a drag-to-timebox week view, mock data),
`extensions/github` (issues/PRs with GitHub-flavored rows, mock data). See
[ARCHITECTURE.md](ARCHITECTURE.md) and each extension's README.

```sh
make extensions                    # build in-tree syncers + web bundles
cp -r extensions/gcal ~/.taskd/extensions/    # (with a built binary + config)
```

## Layout

```
proto/task/task.proto  the public API — one service, fully commented; read this first
gen/                   generated Go (committed; `make generate` to refresh)
internal/store/        SQLite: filters→SQL, keyset pagination, sync upsert
internal/server/       TaskService handlers + watch fan-out
internal/extension/    extension host: supervise syncers, serve web bundles
internal/daemon/       assembly: config, listeners, extension startup
internal/webui/        go:embed of the built web bundle (webui build tag)
pkg/client/            dialing helper every Go client uses
pkg/syncer/            public Go SDK for writing syncers
web/                   web frontend (React + TS + connect-es, Tailwind)
web/extension-api/     the TS contract extensions build their frontend against
extensions/            in-tree extensions (ics, gcal, github)
cmd/taskd, cmd/task, cmd/task-mcp
```

## Development

```sh
make lint         # buf lint + gofmt + go vet
make test         # go test -race ./...
make generate     # after proto changes (needs buf, protoc-gen-go, protoc-gen-connect-go)
make extensions   # build in-tree extension syncers + web bundles
```

Conventions that matter:

- **The proto is the contract** and is deliberately unversioned: it changes
  freely until it settles, then freezes and only grows additively
  (DESIGN.md §3).
- **Field ownership replaces sync machinery.** On synced tasks the source
  owns `title/due/completed/external_data`; the user owns
  `labels/notes/user_data`. No code may cross that line (DESIGN.md §5).
- **Only the store touches SQL; everything else speaks the API.** Extensions
  included — a syncer has no more access than any other client.
