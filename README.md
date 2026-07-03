# taskcore

An extensible personal task tracker: one local daemon that centralizes tasks,
bugs, calendar events, and anything else trackable from many sources, with
one query language, one rules engine, and one editing surface. Everything —
CLI, TUI, web UI, MCP server, and every extension — is a gRPC peer of the
daemon. See [DESIGN.md](DESIGN.md) for the full design.

**Status: phases 0–1 complete** — native tasks end to end: daemon, store,
change feed, CEL queries, saved views, CLI, export/backup. Next phases per
DESIGN.md §18: plugin host + first connector, rules engine, intents/outbox,
MCP, TUI/web.

## Quick start

```sh
make build          # builds ./taskd and ./task
./task daemon start # or: ./task daemon run (foreground), or launchd/systemd
./task add "write the calendar connector" -p taskcore -l dev --due 3d
./task ls
./task done <id-prefix>
./task watch        # live change feed
```

Data lives in `~/.local/share/taskd` (override: `TASKD_DIR`): SQLite database,
unix socket (0600 — the socket's file permissions are the security boundary),
pidfile.

## Layout

```
proto/taskcore/v1/   the public API (buf module; breaking changes CI-blocked)
gen/                 generated Go (committed; `make generate` to refresh)
cmd/taskd, cmd/task  daemon and CLI binaries
internal/            one package per design-doc engine:
  clock/    injectable time + ULID generation (determinism is a requirement)
  store/    SQLite: items, event log, views, backup — one tx per mutation
  feed/     the differ (edge detector) and the watch hub
  query/    CEL engine, virtual fields, SQL pushdown
  server/   gRPC services: API semantics live here
  daemon/   assembly: socket, retention, seeded views
pkg/taskclient/      client SDK: dial, pagination, watch-with-resync
plugins/             connector binaries (phase 2+)
```

## Development

```sh
make lint   # buf lint + gofmt + go vet
make test   # go test -race ./...
make generate  # after proto changes (needs buf, protoc-gen-go{,-grpc})
```

Conventions that matter:

- **Protos are the contract.** `taskcore/v1` is append-only; `buf breaking`
  runs in CI against `main`.
- **No wall clocks, no bare entropy.** Everything takes `internal/clock`
  interfaces; tests use the fake clock and seeded IDs.
- **Mirrors are downstream-only; todos are user-only.** No code may cross
  that ownership line — it is what keeps this system free of sync-conflict
  machinery (DESIGN.md §2).
- **Events are sacred.** The differ (`internal/feed/diff.go`) has a
  completeness guard: adding a field to `item.proto` fails tests until the
  differ learns it. That is intentional.
