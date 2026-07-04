# github — issue/PR sync extension (MOCK)

Presents GitHub issues and pull requests as taskd tasks. Each item becomes a
task with `source: "github"`, a `bug` label, no due date, and its repo, number,
kind, state, author, comment count and (for PRs) additions/deletions carried in
`external_data`. Closed and merged items read as completed; open and draft ones
stay active. The batch is a full snapshot, so items that disappear are pruned.

> **MOCK DATA.** This syncer generates a fixed, deterministic set of ~18
> issues/PRs across a few repos entirely offline — no GitHub API, no
> credentials, no network. It exists to exercise the web presenter/detail
> surface. A real version swaps `mock.go` for the GitHub REST API (listing
> issues and PRs per repo) and could add close-on-complete write-back via
> `syncer.WatchChanges` (task completed locally -> close the remote issue).
> Everything else — config, the single `github` source, the full-snapshot
> `UpsertExternalTasks` call — stays identical.

The web half registers a presenter: rows get a state icon, a `<repo>#<number>`
subtitle, and kind/state chips; the detail panel shows a colored kind+state
badge, author, comments, PR diffstat, the body when present, and an "Open on
GitHub" link.

## Install

```sh
make extensions                              # builds ./task-sync-github
node extensions/build-web.mjs extensions/github   # builds web/main.js
mkdir -p ~/.taskd/extensions/github
cp -r manifest.json task-sync-github web ~/.taskd/extensions/github/
cp config.yaml.example ~/.taskd/extensions/github/config.yaml   # optional
```

Restart `taskd`. It supervises the syncer (restarting with backoff), logs lines
prefixed `ext github:`, and serves the web bundle at `/ext/github/main.js`.
Nothing about the daemon changes — an extension is a folder it discovers.

## Configure

See [`config.yaml.example`](config.yaml.example). Both keys are optional:
`interval` (Go duration, default `15m`) and `repos` (the repositories the mock
items are spread across, default `taskd/core`, `taskd/web`, `acme/infra`). With
no config file at all the defaults apply.

## Ownership

The source owns each task's title, completion, and `external_data`; you own its
labels and notes. The `bug` label is added on every sync but never removed, and
any labels or notes you add survive every sync.
