# ics — calendar sync extension

Mirrors iCalendar feeds (URLs or local `.ics` files) into taskd as tasks.
Each event becomes a task with `source: "ics:<name>"`, its start as the due
date, and start/end/location carried in `external_data`. Past events read as
completed; recurring events are expanded. The window is the previous day
through 30 days out, synced as a full snapshot (events that leave the window
are pruned).

This is the **reference syncer extension**: a standalone binary that talks
to the daemon only through the public API (`pkg/syncer`), with no privileged
access. It is the template to copy when writing your own integration — in Go
or, since it's just one RPC (`UpsertExternalTasks`), any language.

## Install

```sh
make extensions                          # builds ./task-sync-ics
mkdir -p ~/.taskd/extensions/ics
cp manifest.json task-sync-ics ~/.taskd/extensions/ics/
cp config.yaml.example ~/.taskd/extensions/ics/config.yaml
$EDITOR ~/.taskd/extensions/ics/config.yaml   # add your calendar URL(s)
```

Restart `taskd`. It supervises the syncer (restarting with backoff) and logs
lines prefixed `ext ics:`. Nothing about the daemon changes — an extension is
a folder it discovers, not a code change.

## Configure

See [`config.yaml.example`](config.yaml.example). Each calendar needs a
unique `name` and exactly one of `url` / `path`; `labels` are applied to
every imported event (added, never removed — your own labels survive syncs).
For Google Calendar, use the per-calendar private ICS URL under Settings →
"Secret address in iCal format".

## Ownership

The feed owns each task's title, due date, completion, and `external_data`;
you own its labels and notes. Editing those in any client survives every
sync. Timeboxing (`user_data.timebox`, set by the calendar view) survives too.
