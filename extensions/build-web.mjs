// Bundles one extension's web-src/main.tsx into web/main.js — a single ESM
// file the daemon serves at /ext/<name>/main.js and the app dynamic-imports.
//
// react and react/jsx-runtime are aliased to the host shims so the extension
// shares the app's React instance (a second copy breaks hooks). Types from
// @taskd/extension-api are import-type only and erased, so they need no
// runtime resolution.
//
//   node extensions/build-web.mjs <extension-dir> [--watch]
//
// Run from the repo root; requires web/node_modules (esbuild).

import { build, context } from "../web/node_modules/esbuild/lib/main.js";
import { existsSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const extDir = resolve(process.argv[2] ?? ".");
const watch = process.argv.includes("--watch");

const entry = resolve(extDir, "web-src/main.tsx");
if (!existsSync(entry)) {
  console.error(`build-web: no web-src/main.tsx in ${extDir}`);
  process.exit(1);
}

const shim = (f) => resolve(root, "web/extension-api", f);

/** @type {import("../web/node_modules/esbuild/lib/main.js").BuildOptions} */
const options = {
  entryPoints: [entry],
  outfile: resolve(extDir, "web/main.js"),
  bundle: true,
  format: "esm",
  jsx: "automatic",
  target: "es2022",
  logLevel: "info",
  alias: {
    react: shim("react-shim.js"),
    "react/jsx-runtime": shim("jsx-shim.js"),
  },
};

if (watch) {
  const ctx = await context(options);
  await ctx.watch();
  console.log(`build-web: watching ${extDir}`);
} else {
  await build(options);
}
