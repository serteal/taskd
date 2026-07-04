import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

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
    proxy: {
      "/task.TaskService": "http://127.0.0.1:7517",
      "/healthz": "http://127.0.0.1:7517",
      "/ext": "http://127.0.0.1:7517",
    },
  },
  test: {
    environment: "node",
  },
});
