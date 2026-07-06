// A session-scoped, in-memory undo stack. Mutating actions (complete, delete,
// reschedule, their bulk variants, and detail-panel field edits) push an entry
// whose `run` reverses the change. The reversal can be triggered two ways:
//   - the user clicks a toast's "Undo" button, or
//   - the user presses ⌘Z / Ctrl+Z with no editable field focused (undoLast).
// Whichever fires first marks the entry consumed, so the two paths can never
// double-run the same closure. The stack is capped so a long session can't grow
// it without bound; it is deliberately NOT persisted (a reload starts fresh —
// the closures capture live store handles that don't survive a reload anyway).

export interface UndoEntry {
  readonly label: string;
  readonly run: () => void | Promise<void>;
  consumed: boolean;
}

const STACK_CAP = 50;
const stack: UndoEntry[] = [];

/** Push an undoable action. `run` performs the reversal. Returns the entry so
 *  the caller can route its own toast's Undo button through consumeUndo(),
 *  sharing one consumption path with ⌘Z. */
export function pushUndo(label: string, run: () => void | Promise<void>): UndoEntry {
  const entry: UndoEntry = { label, run, consumed: false };
  stack.push(entry);
  if (stack.length > STACK_CAP) stack.splice(0, stack.length - STACK_CAP);
  return entry;
}

/** Run an entry's reversal at most once, whether reached via ⌘Z or a toast's
 *  Undo button. Returns true only for the call that actually performed it. */
export function consumeUndo(entry: UndoEntry): boolean {
  if (entry.consumed) return false;
  entry.consumed = true;
  void entry.run();
  return true;
}

/** Pop-and-run the most recent not-yet-consumed entry. Returns its label (for
 *  the confirmation toast) or null when there is nothing left to undo. */
export function undoLast(): { label: string } | null {
  for (let i = stack.length - 1; i >= 0; i--) {
    const e = stack[i];
    if (e.consumed) continue;
    consumeUndo(e);
    return { label: e.label };
  }
  return null;
}

/** Test-only: drop all entries so cases don't bleed into one another. */
export function _resetUndo(): void {
  stack.length = 0;
}
