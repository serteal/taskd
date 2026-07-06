import { useSyncExternalStore } from "react";

// A tiny notifications store: transient toasts (optionally carrying an action
// like Undo) and native browser notifications. Same useSyncExternalStore
// pattern as the task store, so React binds without a provider. Everything
// user-facing and ephemeral routes through here — failed writes, extension
// errors, undo prompts, and (later) reminders.

// Test mode (?test=1) keeps toasts on screen so the e2e suite can assert their
// content and click their actions (e.g. Undo) without racing auto-dismiss.
const NO_AUTODISMISS =
  typeof location !== "undefined" && new URLSearchParams(location.search).has("test");

export type ToastKind = "info" | "success" | "error";

export interface ToastAction {
  label: string;
  onClick: () => void;
}

export interface Toast {
  id: number;
  kind: ToastKind;
  message: string;
  action?: ToastAction;
}

class Notifier {
  private toasts: Toast[] = [];
  private listeners = new Set<() => void>();
  private seq = 1;

  subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };

  getSnapshot = (): Toast[] => this.toasts;

  /** Shows a toast; returns its id. Auto-dismisses (longer when it has an
   *  action so there's time to click Undo). duration <= 0 keeps it until
   *  dismissed. */
  toast(t: { kind?: ToastKind; message: string; action?: ToastAction; duration?: number }): number {
    const id = this.seq++;
    this.toasts = [...this.toasts, { id, kind: t.kind ?? "info", message: t.message, action: t.action }];
    this.emit();
    const duration = NO_AUTODISMISS ? 0 : t.duration ?? (t.action ? 7000 : 4000);
    if (duration > 0) setTimeout(() => this.dismiss(id), duration);
    return id;
  }

  error(message: string): number {
    return this.toast({ kind: "error", message, duration: 6000 });
  }

  dismiss(id: number): void {
    const next = this.toasts.filter((x) => x.id !== id);
    if (next.length !== this.toasts.length) {
      this.toasts = next;
      this.emit();
    }
  }

  /** Shows a native browser notification, requesting permission on first use
   *  (never on load). No-op if unsupported or denied. Plumbing for reminders. */
  async browser(title: string, options?: NotificationOptions): Promise<void> {
    if (typeof Notification === "undefined") return;
    let perm = Notification.permission;
    if (perm === "default") {
      try {
        perm = await Notification.requestPermission();
      } catch {
        return;
      }
    }
    if (perm === "granted") new Notification(title, options);
  }

  private emit(): void {
    for (const fn of this.listeners) fn();
  }
}

export const notify = new Notifier();

export function useToasts(): Toast[] {
  return useSyncExternalStore(notify.subscribe, notify.getSnapshot);
}

// --- due-task reminders: user preference ------------------------------------
//
// Off by default so the browser's permission prompt only ever appears after
// an explicit opt-in in Settings. A plain client-side preference (not synced
// to the daemon), like the sidebar-collapse/theme prefs elsewhere in lib/.

const DUE_REMINDERS_KEY = "taskd-notify-due";

export function readDueRemindersEnabled(): boolean {
  try {
    return localStorage.getItem(DUE_REMINDERS_KEY) === "1";
  } catch {
    return false;
  }
}

export function writeDueRemindersEnabled(enabled: boolean): void {
  try {
    localStorage.setItem(DUE_REMINDERS_KEY, enabled ? "1" : "0");
  } catch {
    // storage full/blocked — preference stays session-local this run
  }
}

// --- reminders "last seen" watermark ----------------------------------------
//
// A plain epoch-ms scalar marking the last moment reminders were evaluated. On
// the next load it lets useDueReminders catch up on tasks that became due while
// the app was closed (see missedSince). null = never set (first enable).

const REMINDERS_WATERMARK_KEY = "taskd-reminders-last-seen";

export function readRemindersWatermark(): number | null {
  try {
    const raw = localStorage.getItem(REMINDERS_WATERMARK_KEY);
    if (raw === null) return null;
    const n = Number(raw);
    return Number.isFinite(n) ? n : null;
  } catch {
    return null;
  }
}

export function writeRemindersWatermark(ms: number): void {
  try {
    localStorage.setItem(REMINDERS_WATERMARK_KEY, String(ms));
  } catch {
    // storage full/blocked — watermark stays session-local this run
  }
}

/** Proactively request the Notification permission from a user gesture (the
 *  Settings toggle) rather than waiting for the first actual reminder, so the
 *  browser's prompt appears right when the user opts in. No-op if
 *  unsupported or already decided. */
export async function requestBrowserPermission(): Promise<void> {
  if (typeof Notification === "undefined" || Notification.permission !== "default") return;
  try {
    await Notification.requestPermission();
  } catch {
    /* ignore */
  }
}

// The shape handed to extensions as api.notify.
export const extensionNotify = {
  toast: (t: { kind?: ToastKind; message: string; action?: ToastAction; duration?: number }) =>
    notify.toast(t),
  error: (message: string) => notify.error(message),
  browser: (title: string, options?: NotificationOptions) => notify.browser(title, options),
};
