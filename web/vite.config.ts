import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Repo root (one up from web/) and the extension unit tests that live there.
// Built from import.meta.url so vite.config stays free of node type deps.
const repoRoot = new URL("..", import.meta.url).pathname;
const extTests = new URL("../extensions/**/web-src/**/*.test.ts", import.meta.url).pathname;

// Dev: proxy the API to a running taskd so the browser sees one origin (no
// CORS anywhere). Prod: the build output is embedded into taskd itself
// (internal/webui), which serves UI and API from the same port.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
  },
  server: {
    // Allow vitest to load the extension unit tests that live outside web/.
    fs: { allow: [repoRoot] },
    proxy: {
      "/task.TaskService": "http://127.0.0.1:8888",
      "/admin.AdminService": "http://127.0.0.1:8888",
      "/healthz": "http://127.0.0.1:8888",
      "/version": "http://127.0.0.1:8888",
      "/ext": "http://127.0.0.1:8888",
    },
  },
  test: {
    environment: "node",
    include: ["src/**/*.test.ts", "src/**/*.test.tsx", extTests],
  },
});
