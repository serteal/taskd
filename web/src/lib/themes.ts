// Named-theme catalog + apply engine. A theme is a mode ("light"/"dark") plus
// the eight CSS vars the UI is built on (see index.css `@theme inline`). The
// engine writes these vars inline on <html>, toggles the `.dark` class (so the
// existing `dark:` Tailwind variants keep working), and stamps `data-theme` so
// tests and the future Settings picker can read the active theme.
//
// index.css keeps `:root` = paper and `.dark` = dusk as the first-paint
// fallback; the saved theme is applied synchronously in main.tsx before React
// renders, so there is no flash.

export interface ThemeVars {
  bg: string;
  surface: string;
  ink: string;
  muted: string;
  faint: string;
  line: string;
  accent: string;
  warn: string;
}

export interface Theme {
  id: string;
  label: string;
  /** Grouping for the Settings picker, e.g. "Catppuccin". */
  group: string;
  mode: "light" | "dark";
  vars: ThemeVars;
}

export interface ThemeGroup {
  name: string;
  themes: Theme[];
}

export const STORAGE_KEY = "taskd-theme";
export const DEFAULT_LIGHT = "paper";
export const DEFAULT_DARK = "dusk";

// Order here is the order the Settings picker will show. `paper`/`dusk` copy
// the exact values that used to be hard-coded in index.css, so the default
// look is unchanged. The rest are the well-known palettes.
export const THEMES: Theme[] = [
  // taskd — the built-in "engineering paper" identity.
  {
    id: "paper",
    label: "Paper",
    group: "taskd",
    mode: "light",
    vars: {
      bg: "#f4f4f1",
      surface: "#fdfdfc",
      ink: "#1b1d21",
      muted: "#6e747e",
      faint: "#a2a7b0",
      line: "#e3e3dc",
      accent: "#0b7a6b",
      warn: "#c74e22",
    },
  },
  {
    id: "dusk",
    label: "Dusk",
    group: "taskd",
    mode: "dark",
    vars: {
      bg: "#141519",
      surface: "#1c1e24",
      ink: "#e7e8ea",
      muted: "#8a8f98",
      faint: "#5d6169",
      line: "#2a2d34",
      accent: "#3cbfa4",
      warn: "#e2703f",
    },
  },

  // Catppuccin — accent: mauve, warn: red.
  {
    id: "latte",
    label: "Latte",
    group: "Catppuccin",
    mode: "light",
    vars: {
      bg: "#e6e9ef",
      surface: "#eff1f5",
      ink: "#4c4f69",
      muted: "#6c6f85",
      faint: "#8c8fa1",
      line: "#ccd0da",
      accent: "#8839ef",
      warn: "#d20f39",
    },
  },
  {
    id: "frappe",
    label: "Frappé",
    group: "Catppuccin",
    mode: "dark",
    vars: {
      bg: "#303446",
      surface: "#414559",
      ink: "#c6d0f5",
      muted: "#a5adce",
      faint: "#737994",
      line: "#51576d",
      accent: "#ca9ee6",
      warn: "#e78284",
    },
  },
  {
    id: "macchiato",
    label: "Macchiato",
    group: "Catppuccin",
    mode: "dark",
    vars: {
      bg: "#24273a",
      surface: "#363a4f",
      ink: "#cad3f5",
      muted: "#a5adcb",
      faint: "#6e738d",
      line: "#494d64",
      accent: "#c6a0f6",
      warn: "#ed8796",
    },
  },
  {
    id: "mocha",
    label: "Mocha",
    group: "Catppuccin",
    mode: "dark",
    vars: {
      bg: "#1e1e2e",
      surface: "#313244",
      ink: "#cdd6f4",
      muted: "#a6adc8",
      faint: "#6c7086",
      line: "#45475a",
      accent: "#cba6f7",
      warn: "#f38ba8",
    },
  },

  // Gruvbox — accent: aqua, warn: red.
  {
    id: "gruvbox-light",
    label: "Gruvbox Light",
    group: "Gruvbox",
    mode: "light",
    vars: {
      bg: "#f2e5bc",
      surface: "#fbf1c7",
      ink: "#3c3836",
      muted: "#7c6f64",
      faint: "#928374",
      line: "#d5c4a1",
      accent: "#427b58",
      warn: "#9d0006",
    },
  },
  {
    id: "gruvbox-dark",
    label: "Gruvbox Dark",
    group: "Gruvbox",
    mode: "dark",
    vars: {
      bg: "#282828",
      surface: "#3c3836",
      ink: "#ebdbb2",
      muted: "#a89984",
      faint: "#928374",
      line: "#504945",
      accent: "#8ec07c",
      warn: "#fb4934",
    },
  },

  // Nord — accent: frost, warn: aurora red.
  {
    id: "nord",
    label: "Nord",
    group: "Nord",
    mode: "dark",
    vars: {
      bg: "#2e3440",
      surface: "#3b4252",
      ink: "#eceff4",
      muted: "#7b88a1",
      faint: "#4c566a",
      line: "#434c5e",
      accent: "#88c0d0",
      warn: "#bf616a",
    },
  },

  // Rosé Pine — accent: iris, warn: love.
  {
    id: "rose-pine",
    label: "Rosé Pine",
    group: "Rosé Pine",
    mode: "dark",
    vars: {
      bg: "#191724",
      surface: "#1f1d2e",
      ink: "#e0def4",
      muted: "#908caa",
      faint: "#6e6a86",
      line: "#403d52",
      accent: "#c4a7e7",
      warn: "#eb6f92",
    },
  },
  {
    id: "rose-pine-dawn",
    label: "Rosé Pine Dawn",
    group: "Rosé Pine",
    mode: "light",
    vars: {
      bg: "#faf4ed",
      surface: "#fffaf3",
      ink: "#575279",
      muted: "#797593",
      faint: "#9893a5",
      line: "#dfdad9",
      accent: "#907aa9",
      warn: "#b4637a",
    },
  },

  // Solarized — accent: blue, warn: orange.
  {
    id: "solarized-light",
    label: "Solarized Light",
    group: "Solarized",
    mode: "light",
    vars: {
      bg: "#eee8d5",
      surface: "#fdf6e3",
      ink: "#586e75",
      muted: "#657b83",
      faint: "#93a1a1",
      line: "#ddd6c1",
      accent: "#268bd2",
      warn: "#cb4b16",
    },
  },
  {
    id: "solarized-dark",
    label: "Solarized Dark",
    group: "Solarized",
    mode: "dark",
    vars: {
      bg: "#002b36",
      surface: "#073642",
      ink: "#93a1a1",
      muted: "#839496",
      faint: "#586e75",
      line: "#0d4a57",
      accent: "#268bd2",
      warn: "#cb4b16",
    },
  },
];

export const THEME_BY_ID: Record<string, Theme> = Object.fromEntries(
  THEMES.map((t) => [t.id, t]),
);

/** Themes grouped for the Settings picker, preserving THEMES order. */
export const THEME_GROUPS: ThemeGroup[] = (() => {
  const order: string[] = [];
  const byGroup = new Map<string, Theme[]>();
  for (const t of THEMES) {
    let bucket = byGroup.get(t.group);
    if (!bucket) {
      bucket = [];
      byGroup.set(t.group, bucket);
      order.push(t.group);
    }
    bucket.push(t);
  }
  return order.map((name) => ({ name, themes: byGroup.get(name)! }));
})();

/** Apply a theme to <html>: inline CSS vars + `.dark` class + `data-theme`.
 *  Idempotent and safe to call before React mounts. Returns the resolved
 *  theme (falls back to paper for an unknown id). */
export function applyTheme(id: string): Theme {
  const theme = THEME_BY_ID[id] ?? THEME_BY_ID[DEFAULT_LIGHT];
  if (typeof document === "undefined") return theme;
  const root = document.documentElement;
  const s = root.style;
  const v = theme.vars;
  s.setProperty("--bg", v.bg);
  s.setProperty("--surface", v.surface);
  s.setProperty("--ink", v.ink);
  s.setProperty("--muted", v.muted);
  s.setProperty("--faint", v.faint);
  s.setProperty("--line", v.line);
  s.setProperty("--accent", v.accent);
  s.setProperty("--warn", v.warn);
  root.classList.toggle("dark", theme.mode === "dark");
  root.dataset.theme = theme.id;
  return theme;
}

/** The theme to use on first load: the saved id, migrating the legacy
 *  boolean/"light"/"dark" values of the same localStorage key, else the id
 *  matching `prefers-color-scheme`. */
export function resolveInitialThemeId(): string {
  let saved: string | null = null;
  try {
    saved = localStorage.getItem(STORAGE_KEY);
  } catch {
    /* storage unavailable */
  }
  if (saved) {
    // Legacy values from the old binary toggle.
    if (saved === "light" || saved === "false") return DEFAULT_LIGHT;
    if (saved === "dark" || saved === "true") return DEFAULT_DARK;
    if (Object.prototype.hasOwnProperty.call(THEME_BY_ID, saved)) return saved;
  }
  let prefersDark = false;
  try {
    prefersDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
  } catch {
    /* matchMedia unavailable */
  }
  return prefersDark ? DEFAULT_DARK : DEFAULT_LIGHT;
}
