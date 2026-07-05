import type { ReactNode } from "react";

// A small, hand-authored SVG icon set — no dependency. Paths are Lucide/
// Feather-style (24×24, stroked, round caps) drawn with `currentColor`, so an
// icon takes the color of the surrounding text and works in both themes.
// Extensions reach these through api.icon(name); they can also inline their
// own <svg> anywhere a ReactNode icon is accepted.

export type IconName =
  | "plus"
  | "star"
  | "moon"
  | "panel"
  | "sort"
  | "chevron-right"
  | "hash"
  | "tag"
  | "swap"
  | "open"
  | "check"
  | "calendar"
  | "clock"
  | "pencil"
  | "trash"
  | "diamond"
  | "circle"
  | "flag"
  | "inbox"
  | "search"
  | "list"
  | "board"
  | "x";

const PATHS: Record<IconName, ReactNode> = {
  plus: <path d="M5 12h14M12 5v14" />,
  star: <path d="M12 2.5l2.9 5.9 6.5.9-4.7 4.6 1.1 6.5-5.8-3-5.8 3 1.1-6.5L2.6 9.3l6.5-.9z" />,
  moon: <path d="M12 3a6.4 6.4 0 0 0 9 9 9 9 0 1 1-9-9z" />,
  panel: (
    <>
      <rect x="3" y="3" width="18" height="18" rx="2" />
      <path d="M15 3v18" />
    </>
  ),
  sort: <path d="M21 16l-4 4-4-4M17 20V4M3 8l4-4 4 4M7 4v16" />,
  "chevron-right": <path d="M9 18l6-6-6-6" />,
  hash: <path d="M4 9h16M4 15h16M10 3L8 21M16 3l-2 18" />,
  tag: (
    <>
      <path d="M20.59 13.41l-7.17 7.17a2 2 0 0 1-2.83 0L2 12V2h10l8.59 8.59a2 2 0 0 1 0 2.82z" />
      <path d="M7 7h.01" />
    </>
  ),
  swap: <path d="M8 3L4 7l4 4M4 7h16M16 21l4-4-4-4M20 17H4" />,
  open: <path d="M7 17L17 7M7 7h10v10" />,
  check: <path d="M20 6L9 17l-5-5" />,
  calendar: (
    <>
      <rect x="3" y="4" width="18" height="18" rx="2" />
      <path d="M8 2v4M16 2v4M3 10h18" />
    </>
  ),
  clock: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 2" />
    </>
  ),
  pencil: (
    <>
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z" />
    </>
  ),
  trash: <path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6M10 11v6M14 11v6" />,
  diamond: <path d="M12 2l10 10-10 10L2 12z" />,
  circle: <circle cx="12" cy="12" r="8.5" />,
  flag: <path d="M4 15s1-1 4-1 5 2 8 2 4-1 4-1V3s-1 1-4 1-5-2-8-2-4 1-4 1zM4 22V15" />,
  inbox: (
    <>
      <path d="M22 12h-6l-2 3h-4l-2-3H2" />
      <path d="M5.45 5.11L2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z" />
    </>
  ),
  search: (
    <>
      <circle cx="11" cy="11" r="8" />
      <path d="M21 21l-4.3-4.3" />
    </>
  ),
  list: <path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01" />,
  board: (
    <>
      <rect x="3" y="3" width="18" height="18" rx="2" />
      <path d="M9 3v18M15 3v18" />
    </>
  ),
  x: <path d="M18 6L6 18M6 6l12 12" />,
};

export function Icon({
  name,
  size = 16,
  className,
  strokeWidth = 2,
}: {
  name: IconName;
  size?: number;
  className?: string;
  strokeWidth?: number;
}) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={`inline-block shrink-0 ${className ?? ""}`}
      aria-hidden
    >
      {PATHS[name]}
    </svg>
  );
}

export const ICON_NAMES = Object.keys(PATHS) as IconName[];
