import { useEffect, useRef, useState } from "react";
import {
  ACTIONS,
  ACTION_BY_ID,
  addBinding,
  isOverridden,
  prettyBinding,
  removeBinding,
  resetAction,
  resetAllBindings,
  strokeOf,
  toStoredBinding,
  useKeymap,
} from "../lib/keymap";
import { notify } from "../lib/notify";
import { Icon } from "./icons";

// Settings → Keybindings: every keymap action, grouped, with its bindings as
// removable chips and a recorder to add more. Recording captures real
// keystrokes: one stroke normally; a bare letter waits briefly for a second
// stroke so chords like "g i" can be taped. Escape cancels, Enter finalizes a
// pending chord early. Assigning a binding that another action holds moves it
// (with a toast) — a binding lives on at most one action.

const CHORD_WAIT_MS = 900;

export function KeybindingsPage() {
  const keymap = useKeymap();
  const groups = [...new Set(ACTIONS.map((a) => a.group))];
  const anyOverridden = ACTIONS.some((a) => isOverridden(a.id));

  return (
    <section data-testid="settings-keybindings">
      <div className="mb-2 flex items-start justify-between gap-3">
        <div>
          <h3 className="text-[13px] font-semibold text-ink">Keybindings</h3>
          <p className="mt-0.5 text-[12px] text-mute">
            Click + to record a shortcut — two-key sequences like{" "}
            <kbd className="rounded border border-line px-1 font-mono text-[10.5px]">g</kbd>{" "}
            <kbd className="rounded border border-line px-1 font-mono text-[10.5px]">i</kbd> work
            too. A shortcut can live on only one action.
          </p>
        </div>
        <button
          onClick={() => {
            resetAllBindings();
            notify.toast({ kind: "info", message: "Keybindings restored to defaults" });
          }}
          disabled={!anyOverridden}
          data-testid="keybindings-reset-all"
          className="shrink-0 rounded-md border border-line px-2.5 py-1 text-[12px] text-mute hover:border-mute hover:text-ink disabled:cursor-default disabled:opacity-40"
        >
          Restore defaults
        </button>
      </div>

      {groups.map((g) => (
        <div key={g} className="mb-4">
          <div className="mb-1.5 font-mono text-[10px] uppercase tracking-[0.16em] text-faint">
            {g}
          </div>
          <div className="overflow-hidden rounded-lg border border-line">
            {ACTIONS.filter((a) => a.group === g).map((a, i) => (
              <BindingRow key={a.id} actionId={a.id} bindings={keymap[a.id] ?? []} first={i === 0} />
            ))}
          </div>
        </div>
      ))}

      <div className="rounded-lg border border-dashed border-line px-3 py-2.5">
        <div className="mb-1 text-[12px] font-medium text-ink">Fixed keys</div>
        <ul className="space-y-0.5 text-[11.5px] text-mute">
          <li>
            <kbd className="rounded border border-line px-1 font-mono text-[10.5px]">Esc</kbd> —
            close overlays, clear the selection
          </li>
          <li>
            <kbd className="rounded border border-line px-1 font-mono text-[10.5px]">↑↓</kbd> /{" "}
            <kbd className="rounded border border-line px-1 font-mono text-[10.5px]">^j ^k</kbd> /{" "}
            <kbd className="rounded border border-line px-1 font-mono text-[10.5px]">^n ^p</kbd> —
            move inside menus, autocomplete and the palette
          </li>
          <li>
            <kbd className="rounded border border-line px-1 font-mono text-[10.5px]">Tab</kbd> /{" "}
            <kbd className="rounded border border-line px-1 font-mono text-[10.5px]">⏎</kbd> —
            accept the highlighted suggestion
          </li>
        </ul>
      </div>
    </section>
  );
}

function BindingRow({
  actionId,
  bindings,
  first,
}: {
  actionId: string;
  bindings: string[];
  first: boolean;
}) {
  const def = ACTION_BY_ID.get(actionId)!;
  const overridden = isOverridden(actionId);
  const [recording, setRecording] = useState(false);

  const commit = (binding: string) => {
    setRecording(false);
    const movedFrom = addBinding(actionId, binding);
    if (movedFrom) {
      const other = ACTION_BY_ID.get(movedFrom);
      notify.toast({
        kind: "info",
        message: `${prettyBinding(binding)} moved here from “${other?.title ?? movedFrom}”`,
      });
    }
  };

  return (
    <div
      data-testid="binding-row"
      data-action={actionId}
      className={`flex items-center gap-3 px-3 py-2 ${first ? "" : "border-t border-line/70"}`}
    >
      <span className="min-w-0 flex-1 truncate text-[13px] text-ink">{def.title}</span>
      <span className="flex flex-wrap items-center justify-end gap-1">
        {bindings.map((b) => (
          <span
            key={b}
            data-testid="binding-chip"
            className="inline-flex items-center gap-1 rounded-md border border-line bg-paper px-1.5 py-0.5 font-mono text-[11.5px] text-ink"
          >
            {prettyBinding(b)}
            <button
              onClick={() => removeBinding(actionId, b)}
              aria-label={`Remove ${prettyBinding(b)} from ${def.title}`}
              className="-mr-0.5 rounded px-0.5 text-mute hover:text-warn"
            >
              ×
            </button>
          </span>
        ))}
        {bindings.length === 0 && (
          <span className="font-mono text-[11px] text-faint">unbound</span>
        )}
        {recording ? (
          <Recorder onCommit={commit} onCancel={() => setRecording(false)} />
        ) : (
          <button
            onClick={() => setRecording(true)}
            aria-label={`Add shortcut for ${def.title}`}
            data-testid="binding-add"
            className="rounded-md border border-dashed border-line px-1.5 py-0.5 text-mute hover:border-mute hover:text-ink"
          >
            <Icon name="plus" size={11} />
          </button>
        )}
        <button
          onClick={() => resetAction(actionId)}
          aria-label={`Reset ${def.title} to default`}
          title="Reset to default"
          data-testid="binding-reset"
          className={`rounded px-1 text-mute hover:text-ink ${overridden ? "" : "invisible"}`}
        >
          <Icon name="repeat" size={11} />
        </button>
      </span>
    </div>
  );
}

/** The live capture chip. Grabs keydowns at the window (capture phase) so the
 *  keys being taped never trigger the app underneath. */
function Recorder({
  onCommit,
  onCancel,
}: {
  onCommit: (binding: string) => void;
  onCancel: () => void;
}) {
  const [strokes, setStrokes] = useState<string[]>([]);
  const strokesRef = useRef(strokes);
  strokesRef.current = strokes;
  const timer = useRef<number | undefined>(undefined);

  useEffect(() => {
    const finalize = () => {
      if (strokesRef.current.length > 0) onCommit(toStoredBinding(strokesRef.current));
      else onCancel();
    };
    const onKey = (e: KeyboardEvent) => {
      e.preventDefault();
      e.stopPropagation();
      if (e.key === "Escape") {
        onCancel();
        return;
      }
      if (e.key === "Enter" && strokesRef.current.length > 0) {
        finalize();
        return;
      }
      const stroke = strokeOf(e);
      if (!stroke) return; // bare modifier — wait for the full combo
      const next = [...strokesRef.current, stroke].slice(-2);
      setStrokes(next);
      window.clearTimeout(timer.current);
      // A bare letter may be a chord prefix — leave the tape rolling briefly.
      const mayChord = next.length === 1 && /^[a-z]$/.test(stroke);
      if (mayChord) {
        timer.current = window.setTimeout(finalize, CHORD_WAIT_MS);
      } else {
        onCommit(toStoredBinding(next));
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => {
      window.removeEventListener("keydown", onKey, true);
      window.clearTimeout(timer.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <span
      data-testid="binding-recorder"
      className="inline-flex items-center gap-1 rounded-md border border-accent/60 bg-accent/[.08] px-1.5 py-0.5 font-mono text-[11.5px] text-accent"
    >
      {strokes.length === 0 ? "press keys…" : strokes.map((s) => prettyBinding(s)).join(" ")}
      <span className="text-[10px] text-accent/70">esc cancels</span>
    </span>
  );
}
