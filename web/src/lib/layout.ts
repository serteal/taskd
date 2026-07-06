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
