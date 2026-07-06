import { useEffect, useMemo, useRef, useState } from "react";
import { useSnapshot, useThemeSelector } from "../lib/hooks";
import type { Theme } from "../lib/themes";
import {
  useExtensions,
  useDaemonVersion,
  toggleExtension,
  extensionForSource,
  type ExtInfo,
} from "../lib/admin";
import { notify, requestBrowserPermission } from "../lib/notify";
import { readDefaultView, writeDefaultView, viewTitle, DEFAULT_VIEWS, type DefaultView } from "../lib/views";
import { Icon } from "./icons";

/** The cog/gear glyph, matching the hand-authored icon set's stroke style.
 *  Lives here (not in the shared `icons` set, which this wave doesn't own) and
 *  is reused by the Sidebar entry and the ⌘K command. */
export function GearIcon({ size = 16, className }: { size?: number; className?: string }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={`inline-block shrink-0 ${className ?? ""}`}
      aria-hidden
    >
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
    </svg>
  );
}

export function Settings({
  onClose,
  onOpenShortcuts,
  notifyDue,
  onToggleNotifyDue,
}: {
  onClose: () => void;
  onOpenShortcuts: () => void;
  notifyDue: boolean;
  onToggleNotifyDue: () => void;
}) {
  const cardRef = useRef<HTMLDivElement>(null);

  // Keep the latest onClose without re-running the focus-trap effect (which
  // would otherwise yank focus back on every parent re-render, e.g. the clock).
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  // Focus the dialog once on mount so the screen reader announces it and the
  // trap has an anchor.
  useEffect(() => {
    cardRef.current?.focus();
  }, []);

  // Focus trap + Escape, scoped to the card. Attached once.
  useEffect(() => {
    const card = cardRef.current;
    if (!card) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onCloseRef.current();
        return;
      }
      if (e.key !== "Tab") return;
      const els = [...card.querySelectorAll<HTMLElement>(
        'button:not([disabled]), [href], input:not([disabled]), select, textarea, [tabindex]:not([tabindex="-1"])',
      )].filter((el) => el.offsetParent !== null || el === document.activeElement);
      if (els.length === 0) return;
      const first = els[0];
      const last = els[els.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };
    card.addEventListener("keydown", onKey);
    return () => card.removeEventListener("keydown", onKey);
  }, []);

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/30 px-4 py-[8vh]"
      onMouseDown={onClose}
    >
      <div
        ref={cardRef}
        role="dialog"
        aria-modal="true"
        aria-label="Settings"
        tabIndex={-1}
        data-testid="settings"
        className="flex max-h-full w-[640px] max-w-full flex-col overflow-hidden rounded-xl border border-line bg-surface shadow-2xl focus:outline-none"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <header className="flex items-baseline justify-between border-b border-line px-5 py-3.5">
          <h2 className="text-[16px] font-semibold tracking-tight">Settings</h2>
          <button onClick={onClose} className="font-mono text-[12px] text-mute hover:text-ink">
            esc
          </button>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          {/* Functional settings first (Startup → Notifications → Extensions);
              the 12-card theme catalog no longer buries them. */}
          <GeneralSection />
          <NotificationsSection enabled={notifyDue} onToggle={onToggleNotifyDue} />
          <ExtensionsSection />
          <AppearanceSection />
          <AboutSection onOpenShortcuts={onOpenShortcuts} />
        </div>
      </div>
    </div>
  );
}

function SectionTitle({ children, hint }: { children: React.ReactNode; hint?: string }) {
  return (
    <div className="mb-2">
      <h3 className="text-[13px] font-semibold text-ink">{children}</h3>
      {hint && <p className="mt-0.5 text-[12px] text-mute">{hint}</p>}
    </div>
  );
}

// --- Appearance -------------------------------------------------------------

function AppearanceSection() {
  const { themeId, mode, setTheme, toggleMode, groups } = useThemeSelector();
  return (
    <section className="mb-7" data-testid="settings-appearance">
      <SectionTitle hint="Pick a palette — it applies live and is remembered.">
        Appearance
      </SectionTitle>

      <div className="mb-3 inline-flex rounded-lg border border-line p-0.5" role="group" aria-label="Light or dark">
        {(["light", "dark"] as const).map((m) => (
          <button
            key={m}
            onClick={() => {
              if (mode !== m) toggleMode();
            }}
            aria-pressed={mode === m}
            aria-label={`${m} mode`}
            className={`rounded-md px-3 py-1 text-[12.5px] capitalize transition ${
              mode === m ? "bg-accent/12 font-medium text-accent" : "text-mute hover:text-ink"
            }`}
          >
            {m}
          </button>
        ))}
      </div>

      {groups.map((g) => (
        <div key={g.name} className="mb-3">
          <div className="mb-1.5 font-mono text-[10px] uppercase tracking-[0.16em] text-faint">
            {g.name}
          </div>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            {g.themes.map((t) => (
              <ThemeCard key={t.id} theme={t} active={t.id === themeId} onSelect={() => setTheme(t.id)} />
            ))}
          </div>
        </div>
      ))}
    </section>
  );
}

function ThemeCard({ theme, active, onSelect }: { theme: Theme; active: boolean; onSelect: () => void }) {
  const chips = [theme.vars.bg, theme.vars.surface, theme.vars.ink, theme.vars.accent, theme.vars.warn];
  return (
    <button
      onClick={onSelect}
      data-testid="theme-option"
      data-theme-id={theme.id}
      data-active={active}
      aria-pressed={active}
      className={`flex items-center gap-3 rounded-lg border px-3 py-2 text-left transition ${
        active
          ? "border-accent bg-accent/[.06] ring-1 ring-accent/40"
          : "border-line hover:border-mute"
      }`}
    >
      <span
        className="flex shrink-0 overflow-hidden rounded-md border border-line/70"
        aria-hidden
      >
        {chips.map((c, i) => (
          <span key={i} className="h-6 w-3" style={{ backgroundColor: c }} />
        ))}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[13px] font-medium text-ink">{theme.label}</span>
        <span className="block font-mono text-[10px] text-faint">{theme.mode}</span>
      </span>
      {active && <Icon name="check" size={15} className="text-accent" />}
    </button>
  );
}

// --- General ------------------------------------------------------------------

function GeneralSection() {
  const [defaultView, setDefaultView] = useState<DefaultView>(readDefaultView);
  return (
    <section className="mb-7" data-testid="settings-general">
      <SectionTitle hint="Which list a bare load (bookmark, PWA launch) opens to.">
        Startup view
      </SectionTitle>
      <div className="flex flex-wrap gap-1 rounded-lg border border-line p-0.5" role="group" aria-label="Startup view">
        {DEFAULT_VIEWS.map((v) => (
          <button
            key={v}
            onClick={() => {
              setDefaultView(v);
              writeDefaultView(v);
            }}
            aria-pressed={defaultView === v}
            data-testid="default-view-option"
            data-view={v}
            className={`rounded-md px-3 py-1 text-[12.5px] transition ${
              defaultView === v ? "bg-accent/12 font-medium text-accent" : "text-mute hover:text-ink"
            }`}
          >
            {viewTitle({ kind: v })}
          </button>
        ))}
      </div>
    </section>
  );
}

// --- Notifications ------------------------------------------------------------

function NotificationsSection({ enabled, onToggle }: { enabled: boolean; onToggle: () => void }) {
  const supported = typeof Notification !== "undefined";
  const denied = supported && Notification.permission === "denied";

  const toggle = () => {
    onToggle();
    if (!enabled) void requestBrowserPermission();
  };

  return (
    <section className="mb-7" data-testid="settings-notifications">
      <SectionTitle hint="Get a browser notification the moment a task becomes due.">
        Notifications
      </SectionTitle>
      <div className="flex items-center gap-3 rounded-lg border border-line px-3 py-2">
        <div className="min-w-0 flex-1">
          <div className="text-[13px] font-medium text-ink">Due-task reminders</div>
          {!supported ? (
            <div className="text-[11px] text-mute">Not supported in this browser.</div>
          ) : denied ? (
            <div className="text-[11px] text-warn">Blocked — allow notifications for this site to use it.</div>
          ) : (
            <div className="text-[11px] text-mute">{enabled ? "Enabled" : "Disabled"}</div>
          )}
        </div>
        <button
          role="switch"
          aria-checked={enabled}
          aria-label="Due-task reminders"
          disabled={!supported || denied}
          data-testid="notify-due-toggle"
          onClick={toggle}
          className={`relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition ${
            enabled ? "bg-accent" : "bg-line"
          } ${!supported || denied ? "opacity-50" : ""}`}
        >
          <span
            className={`inline-block h-4 w-4 transform rounded-full bg-white shadow ring-1 ring-black/5 transition ${
              enabled ? "translate-x-4" : "translate-x-0.5"
            }`}
          />
        </button>
      </div>
    </section>
  );
}

// --- Extensions -------------------------------------------------------------

function ExtensionsSection() {
  const { exts, loading, error, refresh } = useExtensions();
  const snap = useSnapshot();
  const [busy, setBusy] = useState<Set<string>>(new Set());
  const [needsReload, setNeedsReload] = useState(false);

  // Distinct non-local source values (with counts) from the live replica, so
  // each extension row can show which feeds it currently powers.
  const sourceCounts = useMemo(() => {
    const m = new Map<string, number>();
    for (const t of snap.tasks.values()) {
      if (t.source !== "") m.set(t.source, (m.get(t.source) ?? 0) + 1);
    }
    return m;
  }, [snap]);

  const toggle = async (ext: ExtInfo) => {
    const next = !ext.enabled;
    setBusy((s) => new Set(s).add(ext.name));
    try {
      const res = await toggleExtension(ext.name);
      // The syncer stop/start is immediate; an already-loaded web bundle only
      // fully (un)loads on reload, and restart_required means the same.
      if (res.hasWeb || res.restartRequired) setNeedsReload(true);
    } catch {
      notify.error(`Couldn't ${next ? "enable" : "disable"} ${ext.name}.`);
    } finally {
      setBusy((s) => {
        const n = new Set(s);
        n.delete(ext.name);
        return n;
      });
    }
  };

  return (
    <section className="mb-7" data-testid="settings-extensions">
      <SectionTitle hint="Enable or disable installed integrations. Disabling stops its syncer at once.">
        Extensions
      </SectionTitle>

      {error ? (
        <div className="flex items-center justify-between rounded-lg border border-warn/50 px-3 py-2 text-[12.5px] text-mute">
          <span>Couldn't reach the daemon.</span>
          <button onClick={() => void refresh()} className="font-medium text-accent hover:underline">
            Retry
          </button>
        </div>
      ) : exts === null || loading ? (
        <div className="rounded-lg border border-line px-3 py-3 text-[12.5px] text-mute">
          Loading extensions…
        </div>
      ) : exts.length === 0 ? (
        <div className="rounded-lg border border-dashed border-line px-3 py-3 text-[12.5px] text-mute">
          No extensions installed.
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          {exts.map((ext) => (
            <ExtensionRow
              key={ext.name}
              ext={ext}
              sources={[...sourceCounts.entries()]
                .filter(([s]) => extensionForSource(exts, s)?.name === ext.name)
                .sort(([a], [b]) => a.localeCompare(b))}
              busy={busy.has(ext.name)}
              onToggle={() => toggle(ext)}
            />
          ))}
        </div>
      )}

      {needsReload && (
        <div
          data-testid="settings-reload-hint"
          className="mt-2 flex items-center justify-between rounded-lg border border-accent/40 bg-accent/[.06] px-3 py-2 text-[12px] text-mute"
        >
          <span>Some changes need a page reload to finish applying.</span>
          <button
            onClick={() => location.reload()}
            className="font-medium text-accent hover:underline"
          >
            Reload
          </button>
        </div>
      )}
    </section>
  );
}

function CapBadge({ children }: { children: React.ReactNode }) {
  return (
    <span className="rounded border border-line px-1 py-px font-mono text-[9.5px] uppercase tracking-wide text-faint">
      {children}
    </span>
  );
}

function ExtensionRow({
  ext,
  sources,
  busy,
  onToggle,
}: {
  ext: ExtInfo;
  /** [source, itemCount] pairs this extension currently feeds. */
  sources: [string, number][];
  busy: boolean;
  onToggle: () => void;
}) {
  return (
    <div
      className="flex items-center gap-3 rounded-lg border border-line px-3 py-2"
      data-testid="ext-row"
      data-ext={ext.name}
      data-enabled={ext.enabled}
    >
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          <span className="truncate text-[13px] font-medium text-ink">{ext.name}</span>
          {ext.hasSyncer && <CapBadge>syncer</CapBadge>}
          {ext.hasWeb && <CapBadge>web</CapBadge>}
        </div>
        <div className="text-[11px] text-mute">{ext.enabled ? "Enabled" : "Disabled"}</div>
        {sources.length > 0 && (
          <ul className="mt-1 flex flex-col gap-0.5" data-testid="ext-sources">
            {sources.map(([source, count]) => (
              <li key={source} className="font-mono text-[10.5px] text-faint" data-source={source}>
                {source} · {count} item{count === 1 ? "" : "s"}
              </li>
            ))}
          </ul>
        )}
      </div>
      <button
        role="switch"
        aria-checked={ext.enabled}
        aria-label={`${ext.enabled ? "Disable" : "Enable"} ${ext.name}`}
        disabled={busy}
        data-testid="ext-toggle"
        data-ext={ext.name}
        data-enabled={ext.enabled}
        onClick={onToggle}
        className={`relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition ${
          ext.enabled ? "bg-accent" : "bg-line"
        } ${busy ? "opacity-50" : ""}`}
      >
        <span
          className={`inline-block h-4 w-4 transform rounded-full bg-white shadow ring-1 ring-black/5 transition ${
            ext.enabled ? "translate-x-4" : "translate-x-0.5"
          }`}
        />
      </button>
    </div>
  );
}

// --- About ------------------------------------------------------------------

function AboutSection({ onOpenShortcuts }: { onOpenShortcuts: () => void }) {
  const version = useDaemonVersion();
  const origin = typeof window !== "undefined" ? window.location.origin : "";
  return (
    <section data-testid="settings-about">
      <SectionTitle>About</SectionTitle>
      <div className="rounded-lg border border-line px-3 py-3">
        <div className="flex items-baseline gap-2">
          <span className="font-mono text-[14px] font-medium">
            taskd<span className="text-accent">_</span>
          </span>
          <span className="font-mono text-[11px] text-faint" data-testid="app-version">
            {version === "dev" ? "dev" : `v${version}`}
          </span>
        </div>
        <p className="mt-1.5 text-[12.5px] text-mute">
          A local-first todo backend. This app talks to the taskd daemon running on your machine —
          your tasks never leave it.
        </p>
        {origin && (
          <p className="mt-1 font-mono text-[11px] text-faint" data-testid="daemon-address">
            {origin}
          </p>
        )}
        <button
          onClick={onOpenShortcuts}
          className="mt-2.5 inline-flex items-center gap-1.5 text-[12.5px] font-medium text-accent hover:underline"
        >
          Keyboard shortcuts
          <span className="font-mono text-[11px]">?</span>
        </button>
      </div>
    </section>
  );
}
