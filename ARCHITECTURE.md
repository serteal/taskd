# Architecture

How the code is laid out, what the public API is, and how to build against
it. The design rationale lives in [DESIGN.md](DESIGN.md); this is the map of
what exists.

## The one-sentence architecture

A local Go daemon (`taskd`) owns a canonical store and exposes everything
over gRPC on a unix socket; every other component — CLI, MCP server, web UI,
and every plugin — is a gRPC peer, so extensibility is not a plugin API
bolted on the side but the only way anything talks to the core.

## Layout

```
proto/                          the contract (one buf module; breaking changes CI-gated)
  taskcore/v1/                  CLIENT surface: item, event, intent, rule, view + 6 services
  taskcore/plugin/v1/           PLUGIN surface: manifest/handshake, connector, corehost
  taskcore/view/v1/             shared display vocabulary (DisplayValue, Presentation)
  icsplugin/v1, todotxtplugin/v1  plugin-private extension types (in-repo for convenience;
                                  the core never links them — they travel as descriptors)
gen/                            generated Go, committed

cmd/taskd                       the daemon
cmd/task                        the CLI (also `task daemon run|start|stop|status`)
cmd/task-mcp                    the agent frontend (stdio MCP server)

internal/                       one package per engine:
  clock/      injectable time + seeded ULIDs (determinism is a requirement, not a nicety)
  store/      SQLite: items, event log, rules, outbox, views, manifests, meta KV;
              every item mutation commits item + event in ONE transaction
  feed/       the differ (edge detector, with a completeness guard that fails the
              build when item.proto grows an uncovered field), BeforeImage (the
              differ's exact inverse, lockstepped by a roundtrip property test),
              and the watch hub
  query/      CEL engine: filters with SQL pushdown, virtual fields (effective due
              through per-kind facet bindings), dynamic type registration from
              plugin descriptors
  contrib/    contribution registry + core-side renderer — the single place
              display expressions are evaluated
  schema/     kind registry: persists manifests (kinds outlive plugins), enforces
              additive-only type evolution, validates rule templates and
              presentations at registration
  sync/       reconciliation: snapshot diffing, tombstone grace → stale (never
              delete), pinned-mirror refresh, relation wiring, value-based echo
              silencing
  rules/      edge-triggered rules: became/schedule triggers, provenance guards,
              depth-capped cascades, durable cursor, explicit backfill
  intent/     the write path: router + durable outbox (backoff retries, crash-safe
              requeue, retry/discard); only remote-confirmed state touches mirrors
  plugin/     subprocess host: spawn/handshake/supervise per instance, per-instance
              corehost socket, live registry (the intent dispatch surface)
  secret/     macOS keychain / file secret store (plugins own auth; this is the
              optional opaque helper)
  server/     the gRPC services — all API semantics live here
  daemon/     assembly: 0600 socket, config.yaml, engine startup, retention

pkg/taskclient                  client SDK (used by CLI and MCP; TUI/web next)
pkg/taskplugin (+connectortest) plugin SDK + the conformance suite
plugins/ics                     read-only ICS calendar connector (full recurrence)
plugins/todotxt                 bidirectional todo.txt connector (the file is the remote)
```

## Dataflow: a closed loop

- **Mirrors flow one way**: remote → connector `Snapshot`/`Resolve` → sync
  engine → store. Nothing else writes mirror fields.
- **Intents flow the other way**: client/rule/agent → intent router → outbox
  → connector `HandleIntent` → remote → confirmed state → mirror.
- **Todos never leave the machine**; sync cannot touch them (separate
  revision counters make cross-layer false conflicts impossible).
- **One event feed**: every write appends an event (field-level old→new
  values, provenance) that watchers, the rules engine, and frontends consume
  from durable cursors.

## The public API

### Client surface (`taskcore.v1`, gRPC over the unix socket)

| Service | Purpose |
|---|---|
| `ItemService` | CRUD; `QueryItems` (CEL filter, pagination, optional `RenderSpec` → server-rendered rows); `Watch` (resumable feed); `LinkItem` (attach-to-remote) |
| `ViewService` | saved views CRUD; `ListPresentations` (merged display contributions) |
| `RuleService` | rules CRUD; `DryRunRule`; `BackfillRule`; `ListRuleTemplates` |
| `IntentService` | `InvokeIntent` (bounded-wait remote write); list/retry/discard the outbox |
| `SchemaService` | kinds, facet bindings, and per-kind type descriptors (clients register them dynamically — no plugin linking, ever) |
| `AdminService` | Ping (version/cursor handshake); online Backup |

Contract semantics beyond the RPC shapes:

- An `Item` is a connector-owned `Mirror` plus a user-owned `Todo`. Core task
  state is binary (todo/completed); all richer ontology is labels, projects,
  rules, and views.
- One `Update` touches one layer. `mirror.title` masks become rename intents
  (the response's `intent` field reports CONFIRMED vs QUEUED). Everything
  else on the mirror is read-only.
- Item ids resolve by unique prefix on every RPC. Writes carry provenance
  from the `x-task-client` metadata header.
- `Watch` events carry field-level old→new values; an expired cursor fails
  with `FAILED_PRECONDITION`/`CURSOR_EXPIRED` and the client resyncs via
  `QueryItems` (the returned `cursor` makes snapshot-then-follow gapless).
- The CEL environment (filters, rules, bindings, contributions): `item` plus
  `completed, labels, project, kind, state, stale, title, due, has_due,
  snoozed_until, snoozed, now` — `due` is the effective due (user override,
  else the kind's facet binding).
- Standard intents: `rename, add_comment, delete, set_due, set_start,
  assign, set_priority, set_completed`.

### Plugin surface (`taskcore.plugin.v1`)

The host runs one process per connector instance with two env vars:
`TASKPLUGIN_SOCKET` (the plugin serves `PluginService` + `ConnectorService`
here) and `TASKPLUGIN_COREHOST_SOCKET` (the core serves the least-privilege
corehost API here — currently the keychain-backed `SecretService`,
namespaced per instance). The **manifest is the whole integration**: kinds +
facet bindings + typed extension descriptors + rule templates + display
presentations, all validated at registration and persisted so items stay
readable after a plugin is gone. Connectors declare SNAPSHOT or INCREMENTAL
enumeration and which intents they handle per kind; they own translation and
identity, never reconciliation.

### Go SDKs

- `pkg/taskclient`: `Dial` (provenance attached), `QueryAll` (pagination),
  `WatchItems` (the resync loop, written once), `SyncTypes` (register plugin
  descriptors so protojson of extension payloads works clientside).
- `pkg/taskplugin`: implement `Connector` (+ optional `Resolver`,
  `IntentHandler`), call `Serve`; `DescriptorSet` builds manifest type sets;
  `connectortest.RunConformance` is the behavioral contract every connector
  must pass.

Non-Go clients and plugins speak the protos directly.

## How it's used

- **Person, via CLI**: `task daemon start`, `add/ls/done/edit/watch`,
  connectors via `$TASKD_DIR/config.yaml` + a binary in `plugins/`, rules
  via `task rule apply -f rules.yaml`, remote writes via `task rename` /
  `task pending` / `task retry`, attach via `task link`.
- **Agent, via MCP**: `task-mcp` exposes tools that teach the filter
  language, views as resources, typed items for plugin kinds, and refuses
  destructive intents unless allowlisted (`TASKMCP_ALLOW_DESTRUCTIVE`).
- **Plugin author**: 2–3 SDK interfaces + a manifest; the todotxt connector
  is a complete bidirectional reference at ~550 lines plus a parser.
- **Frontend author**: dial `taskclient`, query with a `RenderSpec`, paint
  `DisplayValue`s (semantic tones, never widgets), stay live via `WatchItems`,
  discover per-kind columns via `ListPresentations`. Frontends never
  evaluate expressions and never talk to plugins.

## Invariants that must survive any change

1. Mirrors hold only remote-confirmed truth; todos are user-only. No code
   crosses that line — it is why there is no conflict engine.
2. The event log and item state commit atomically; the differ and
   `BeforeImage` stay exact inverses (guarded by tests).
3. `taskcore/*/v1` protos evolve additively; buf breaking-change checks run
   against `main` in CI.
4. Plugin schemas evolve additively (registration gate); a genuinely new
   shape is a new kind.
5. Everything reads the injectable clock; nothing calls `time.Now` in engine
   code paths.
6. Destructive remote effects ride explicit, confirmable paths — never
   generic CRUD, never sync, and never agents by default.
