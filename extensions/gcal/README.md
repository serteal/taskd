# gcal — mock Google Calendar extension

A **mock** Google Calendar integration, in two halves:

- a **syncer** that fabricates a realistic current week (Mon–Fri) of calendar
  events — daily standups, meetings, a lunch, a focus block, an all-day
  event — and mirrors them into tasks with `source: "gcal:<account>"`; and
- a **web bundle** that presents those calendar-event tasks nicely and adds a
  **Calendar week view** where you can see events and drag your todos onto the
  grid to timebox them.

> **MOCK DATA.** There is no Google API here. `mock.go` invents the events
> deterministically from the current date; no network, no auth, no keys. To
> make it real you would swap `mock.go` for a Google Calendar API (or the
> per-calendar private ICS URL) fetch that emits the same `ExternalTask`
> shape — the `main.go` wiring and the entire web half stay unchanged. The
> event `external_data` schema (`start`, `end`, `all_day`, `location`,
> `description`) is identical to the [`ics`](../ics) syncer's, so both feed
> the same presenter and week view.

## Install

```sh
make extensions                          # builds ./task-sync-gcal
mkdir -p ~/.taskd/extensions/gcal
cp -r manifest.json task-sync-gcal web ~/.taskd/extensions/gcal/
# config.yaml is OPTIONAL for the mock; copy the example only to customize:
# cp config.yaml.example ~/.taskd/extensions/gcal/config.yaml
```

Restart `taskd`. It supervises the syncer (restarting with backoff) and logs
lines prefixed `ext gcal:`, and serves the web bundle so the **Calendar** view
appears in the app's sidebar. With no `config.yaml`, the syncer prints an info
line and runs a single account named `personal` labelled `calendar`.

## Configure

See [`config.yaml.example`](config.yaml.example). Each account needs a unique
`name`; `labels` are applied to every imported event (added, never removed —
your own labels survive syncs). Because the data is mock, a missing config is
not an error.

## Timeboxing

The Calendar week view lets you drag any unscheduled todo onto an hour cell to
timebox it. That writes `user_data.timebox = { start, end }` (RFC3339) on the
task — user-owned data that sync never touches, so it survives every calendar
refresh. The ✕ on a timebox block clears it.

## Ownership

The mock generator owns each task's title, due date, completion, and
`external_data`; you own its labels, notes, and `user_data` (including the
timebox). Editing those in any client survives every sync.
