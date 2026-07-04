import { notify, useToasts, type ToastKind } from "../lib/notify";

const kindClass: Record<ToastKind, string> = {
  info: "border-line",
  success: "border-accent/50",
  error: "border-warn/60",
};

const kindDot: Record<ToastKind, string> = {
  info: "bg-mute",
  success: "bg-accent",
  error: "bg-warn",
};

// Bottom-center stack of transient toasts. An action (e.g. Undo) renders as a
// button; every toast can be dismissed.
export function ToastStack() {
  const toasts = useToasts();
  if (toasts.length === 0) return null;
  return (
    <div className="pointer-events-none fixed bottom-9 left-1/2 z-50 flex -translate-x-1/2 flex-col items-center gap-1.5">
      {toasts.map((t) => (
        <div
          key={t.id}
          role="status"
          className={`pointer-events-auto flex items-center gap-2.5 rounded-lg border ${kindClass[t.kind]} bg-surface px-3 py-2 text-[12.5px] shadow-lg`}
        >
          <span className={`h-[6px] w-[6px] shrink-0 rounded-full ${kindDot[t.kind]}`} aria-hidden />
          <span className="text-ink">{t.message}</span>
          {t.action && (
            <button
              onClick={() => {
                t.action!.onClick();
                notify.dismiss(t.id);
              }}
              className="font-medium text-accent hover:underline"
            >
              {t.action.label}
            </button>
          )}
          <button
            onClick={() => notify.dismiss(t.id)}
            aria-label="Dismiss"
            className="ml-0.5 font-mono text-[12px] text-faint hover:text-ink"
          >
            ×
          </button>
        </div>
      ))}
    </div>
  );
}
