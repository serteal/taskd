// Left sidebar collapse state — a plain client-side UI preference, sibling
// to the per-view prefs in views.ts but global rather than per-view: whether
// the nav is collapsed doesn't depend on which task list you're looking at.

const KEY = "taskd-sidebar-collapsed";

export function readSidebarCollapsed(): boolean {
  try {
    return localStorage.getItem(KEY) === "1";
  } catch {
    return false;
  }
}

export function writeSidebarCollapsed(collapsed: boolean): void {
  try {
    localStorage.setItem(KEY, collapsed ? "1" : "0");
  } catch {
    // storage full/blocked — collapse state stays session-local this run
  }
}

// The stored collapse preference, or null when the user has never set one.
// Distinguishing "unset" from an explicit "0" lets the first-load default
// depend on the viewport without ever overriding a real preference.
export function readSidebarCollapsedPref(): boolean | null {
  try {
    const raw = localStorage.getItem(KEY);
    if (raw === null) return null;
    return raw === "1";
  } catch {
    return null;
  }
}

// Below this viewport width the sidebar starts collapsed by default.
export const SIDEBAR_COLLAPSE_BREAKPOINT = 768;

// The initial collapse state: a stored preference always wins; otherwise the
// sidebar starts collapsed on a narrow viewport and expanded above it. This
// only changes the DEFAULT — the first paint of a fresh browser — never an
// existing user's saved choice.
export function defaultSidebarCollapsed(viewportWidth: number): boolean {
  const pref = readSidebarCollapsedPref();
  if (pref !== null) return pref;
  return viewportWidth < SIDEBAR_COLLAPSE_BREAKPOINT;
}

// --- Resizable panel widths -------------------------------------------------
// Drag-resizable widths for the three side panels (left sidebar, detail panel,
// extension panel wrappers). Each is a plain scalar localStorage key — a single
// pixel width with no shape to evolve, so unlike the structured stores in
// storage.ts it isn't enveloped/versioned; a non-numeric or non-positive stored
// value simply falls back to the default. The clamp is viewport-relative: a hard
// px range AND a fraction of the current viewport, so a panel resized wide on a
// big screen re-clamps for DISPLAY when the window shrinks, without overwriting
// the stored preference.

export interface WidthBounds {
  /** Hard lower bound in px (a legibility floor that always wins). */
  min: number;
  /** Hard upper bound in px. */
  max: number;
  /** Upper bound as a fraction of the viewport width (0–1). */
  vw: number;
  /** Reset / first-use width. */
  default: number;
}

export const SIDEBAR_WIDTH: WidthBounds = { min: 180, max: 400, vw: 0.4, default: 208 };
export const DETAIL_WIDTH: WidthBounds = { min: 280, max: 560, vw: 0.5, default: 340 };
// Extension panels share one clamp; the per-panel default is the registered
// `width ?? 300`, so `default` here is only the fallback for a panel that
// registers no width.
export const PANEL_WIDTH: WidthBounds = { min: 220, max: 600, vw: 0.5, default: 300 };

/** Clamp a desired width to a panel's bounds for the given viewport. The
 *  effective maximum is the smaller of the hard px max and the viewport
 *  fraction; the px min is a floor that always wins (so a very narrow viewport
 *  can't crush a panel below legibility). Pure — no DOM — so it unit-tests. */
export function clampWidth(width: number, bounds: WidthBounds, viewportWidth: number): number {
  const cap = Math.min(bounds.max, Math.floor(bounds.vw * viewportWidth));
  const hi = Math.max(bounds.min, cap);
  const clamped = Math.min(Math.max(width, bounds.min), hi);
  return Math.round(clamped);
}

const SIDEBAR_WIDTH_KEY = "taskd-sidebar-width";
const DETAIL_WIDTH_KEY = "taskd-detail-width";
const panelWidthKey = (id: string) => `taskd-panel-width:${id}`;

// Read a stored pixel width, falling back to `fallback` for a missing,
// non-numeric or non-positive value. The stored preference is returned as-is
// (not clamped) — the display clamp applies the viewport-relative bounds at
// render time, so a wide preference survives a temporary narrow viewport.
function readWidth(key: string, fallback: number): number {
  try {
    const raw = localStorage.getItem(key);
    if (raw === null) return fallback;
    const n = Number(raw);
    return Number.isFinite(n) && n > 0 ? n : fallback;
  } catch {
    return fallback;
  }
}

function writeWidth(key: string, width: number): void {
  try {
    localStorage.setItem(key, String(Math.round(width)));
  } catch {
    // storage full/blocked — width stays session-local this run
  }
}

export function readSidebarWidth(): number {
  return readWidth(SIDEBAR_WIDTH_KEY, SIDEBAR_WIDTH.default);
}
export function writeSidebarWidth(width: number): void {
  writeWidth(SIDEBAR_WIDTH_KEY, width);
}

export function readDetailWidth(): number {
  return readWidth(DETAIL_WIDTH_KEY, DETAIL_WIDTH.default);
}
export function writeDetailWidth(width: number): void {
  writeWidth(DETAIL_WIDTH_KEY, width);
}

export function readPanelWidth(id: string, fallback: number): number {
  return readWidth(panelWidthKey(id), fallback);
}
export function writePanelWidth(id: string, width: number): void {
  writeWidth(panelWidthKey(id), width);
}

// First-run onboarding: a one-time card shown on a connected, empty replica.
// Dismissing it persists here so it never returns; the plain empty state takes
// over once dismissed. A global client-side preference, like the collapse flag.

const ONBOARDING_KEY = "taskd-onboarding-dismissed";

export function readOnboardingDismissed(): boolean {
  try {
    return localStorage.getItem(ONBOARDING_KEY) === "1";
  } catch {
    return false;
  }
}

export function writeOnboardingDismissed(dismissed: boolean): void {
  try {
    localStorage.setItem(ONBOARDING_KEY, dismissed ? "1" : "0");
  } catch {
    // storage full/blocked — dismissal stays session-local this run
  }
}
