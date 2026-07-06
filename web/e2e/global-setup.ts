import { execSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

// Builds the artifacts the daemon fixture spawns: the embedded web bundle,
// the webui daemon, and the in-tree extensions (syncer binaries + web
// bundles). Rebuilding every run guarantees tests exercise current code —
// set E2E_SKIP_BUILD=1 to skip when iterating on specs only.
const WEB = path.dirname(fileURLToPath(import.meta.url)) + "/..";
const ROOT = path.resolve(WEB, "..");

export default function globalSetup() {
  if (process.env.E2E_SKIP_BUILD) return;
  const run = (cmd: string, cwd: string) => execSync(cmd, { cwd, stdio: "inherit" });
  run("npm run build", WEB);
  // Stamp the version like the Makefile does — this build overwrites ./taskd,
  // and an unstamped binary makes Settings→About lie about the running build.
  const version = execSync("git describe --tags --always --dirty", { cwd: ROOT }).toString().trim() || "dev";
  run(
    `go build -tags webui -ldflags "-X github.com/serteal/taskd/internal/version.Version=${version}" -o taskd ./cmd/taskd`,
    ROOT,
  );
  run("go build -o extensions/gcal/task-sync-gcal ./extensions/gcal", ROOT);
  run("go build -o extensions/github/task-sync-github ./extensions/github", ROOT);
  run("node extensions/build-web.mjs extensions/gcal", ROOT);
  run("node extensions/build-web.mjs extensions/github", ROOT);
}
