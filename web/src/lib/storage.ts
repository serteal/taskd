// Versioned localStorage envelopes for the structured JSON stores (saved
// views, saved filters, per-view prefs). Each value is stored as
// `{ v: <number>, data: <T> }` so a future schema change can migrate an old
// payload instead of silently dropping or corrupting it. Plain scalar keys
// (theme id, default view, sidebar-collapse flag) don't use this — they hold a
// single primitive with no shape to evolve.
//
// Read semantics:
//   - unreadable storage / non-JSON → null (caller falls back to its default)
//   - envelope at the current version → its `data`
//   - envelope at a LOWER version → migrate, rewrite in the new envelope, return
//   - envelope at a HIGHER version → null (a newer client wrote it; leave it be
//     rather than downgrade-and-corrupt)
//   - a legacy pre-envelope blob (no `v`) → hand the bare parsed value to
//     `migrate` (default: pass it straight through as the data), then rewrite it
//     enveloped so the upgrade is a one-time event.

export interface VersionedOptions<T> {
  version: number;
  /** Upgrade an older payload to the current shape. `fromVersion` is the stored
   *  envelope version, or null for a legacy pre-envelope blob. Return null to
   *  discard an unmigratable value. Omit to pass values through unchanged. */
  migrate?: (data: unknown, fromVersion: number | null) => T | null;
}

interface Envelope {
  v: number;
  data: unknown;
}

function isEnvelope(x: unknown): x is Envelope {
  return (
    typeof x === "object" &&
    x !== null &&
    typeof (x as { v?: unknown }).v === "number" &&
    "data" in (x as object)
  );
}

export function readVersionedJSON<T>(key: string, opts: VersionedOptions<T>): T | null {
  let raw: string | null;
  try {
    raw = localStorage.getItem(key);
  } catch {
    return null; // storage unavailable
  }
  if (raw === null) return null;

  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null; // corrupt / non-JSON
  }

  const { version, migrate } = opts;

  if (isEnvelope(parsed)) {
    if (parsed.v === version) return parsed.data as T;
    if (parsed.v > version) return null; // newer client — don't touch it
    const upgraded = migrate ? migrate(parsed.data, parsed.v) : (parsed.data as T);
    if (upgraded == null) return null;
    writeVersionedJSON(key, version, upgraded);
    return upgraded;
  }

  // Legacy raw blob (no envelope): treat as fromVersion = null.
  const upgraded = migrate ? migrate(parsed, null) : (parsed as T);
  if (upgraded == null) return null;
  writeVersionedJSON(key, version, upgraded);
  return upgraded;
}

export function writeVersionedJSON<T>(key: string, version: number, data: T): void {
  try {
    localStorage.setItem(key, JSON.stringify({ v: version, data }));
  } catch {
    // storage full/blocked — the caller keeps its in-memory copy this session
  }
}

/** Cross-tab convergence: run `fn` whenever ANOTHER document writes `key` (or
 *  clears storage — `e.key === null`), so a PWA window and a browser tab reload
 *  each other's edits instead of last-writer-wins clobbering them. The browser
 *  never fires `storage` in the writing tab itself, so self-writes can't loop;
 *  a synthetic same-document event (tests) just re-reads, which is idempotent.
 *  No-op outside a window (node unit tests that don't stub one). */
export function onStorageChange(key: string, fn: () => void): void {
  if (typeof window === "undefined" || typeof window.addEventListener !== "function") return;
  window.addEventListener("storage", (e: Event) => {
    const changed = (e as StorageEvent).key;
    if (changed === key || changed === null) fn();
  });
}
