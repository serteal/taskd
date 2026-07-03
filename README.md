# taskcore

An extensible personal task tracker: one local daemon that centralizes tasks,
bugs, calendar events, and anything else trackable from many sources, with
one query language, one rules engine, and one editing surface. Everything —
CLI, TUI, web UI, MCP server, and every extension — is a gRPC peer of the
daemon. See [DESIGN.md](DESIGN.md) for the full design.

**Status: phases 0–5 complete** — native tasks end to end (daemon, store,
change feed, CEL queries, saved views, CLI, export/backup); the plugin
system (subprocess host with supervision, snapshot sync engine with
tombstone grace and value-based echo silencing, persisted schema registry
with an additive-only gate, keychain-backed secret store, plugin SDK +
conformance suite, read-only ICS calendar connector with recurrence); and
the rules engine: edge-triggered `became` rules evaluated against
before/after images (a doorbell, not a level check), cron-scheduled sweeps,
provenance-guarded depth-capped cascades, a durable cursor with idempotent
crash replay, YAML apply/export, DryRun + explicit backfill, and plugin
rule templates. Phase 4 adds the write path: standard intents routed
through connectors with a durable outbox (bounded-wait confirm, backoff
retries, retry/discard), rule write-back (`do: {intent: set_completed}`),
attach-to-remote (`task link`), pinned-mirror refresh, and the first
bidirectional connector — todo.txt (`plugins/todotxt`): your file is the
remote; renames and completions flow both ways. Phase 5 adds the frontend
contribution model and the agent frontend: the closed DisplayValue
vocabulary (semantic tones, never widgets), plugin presentations (columns +
badges) validated at registration, core-side rendering (`task ls` shows
server-computed effective dues and contributed badges for every kind), and
`task-mcp` — an MCP server whose tools teach agents the CEL filter language,
serve views as resources, sync plugin type descriptors so agents get fully
typed items, and refuse destructive intents by default
(TASKMCP_ALLOW_DESTRUCTIVE opts in per intent). Next: phase 6, TUI/web.

```sh
task-mcp --dir ~/.local/share/taskd   # stdio MCP server for agents
```

```sh
task rule apply -f rules.yaml    # became/schedule rules, DESIGN.md §7 shape
task rule template ls            # starter rules shipped by plugins
task rename <id> "new title"     # native: local edit; tracked: rename intent
task pending                     # the outbox: queued / in-flight / failed
task retry <intent-id>           # after fixing whatever broke
task link <id> <instance> <ref>  # attach a native task to a remote object
```

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
pidfile, plugin binaries under `plugins/`, and `config.yaml`:

```yaml
instances:
  - name: cal@personal        # instance name = link namespace
    plugin: ics               # bare name → $TASKD_DIR/plugins/ics
    poll: 5m
    config:
      url: https://calendar.google.com/calendar/ical/…/basic.ics
      horizon_days: 60
```

Mirrored items land un-triaged in the `inbox` view (`task ls inbox`); promote
one by editing it (`task edit <id> -p work`) and it joins your active list —
sync keeps its calendar half fresh and never touches your half.

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
