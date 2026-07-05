import { StrictMode } from "react";
import * as React from "react";
import * as JSXRuntime from "react/jsx-runtime";
import { createRoot } from "react-dom/client";
import "./index.css";
import App from "./App";
import { newTaskClient } from "./lib/client";
import { TaskStore } from "./lib/store";
import { StoreContext } from "./lib/hooks";
import { loadExtensions } from "./lib/extensions";
import { applyTheme, resolveInitialThemeId } from "./lib/themes";

// Apply the saved (or system-preferred) theme to <html> before React mounts so
// there's no flash of the wrong palette. index.css holds paper/dusk as the
// first-paint fallback; this overrides with inline vars + `.dark` + data-theme.
applyTheme(resolveInitialThemeId());

// Extensions bundle with `react` aliased to a shim that reads these — one
// React instance across host and extensions, or hooks break.
declare global {
  interface Window {
    __TASKD_REACT__: typeof React;
    __TASKD_JSX_RUNTIME__: typeof JSXRuntime;
  }
}
window.__TASKD_REACT__ = React;
window.__TASKD_JSX_RUNTIME__ = JSXRuntime;

const store = new TaskStore(newTaskClient());
void loadExtensions(store);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <StoreContext.Provider value={store}>
      <App />
    </StoreContext.Provider>
  </StrictMode>,
);
