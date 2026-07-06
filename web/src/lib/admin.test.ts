import { beforeEach, describe, expect, it, vi } from "vitest";
import { daemonVersion, refreshDaemonVersion } from "./admin";

// The daemon-version store: fetched lazily once per session, but REFRESHABLE —
// after a daemon restart (reconnect) the pills/settings must show the new
// build, not a version cached forever at first load.

function stubVersion(v: string): void {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({ ok: true, json: async () => ({ name: "taskd", version: v }) })),
  );
}

beforeEach(() => {
  vi.unstubAllGlobals();
});

describe("daemonVersion / refreshDaemonVersion", () => {
  it("starts as 'dev' and resolves via refresh", async () => {
    expect(daemonVersion.get()).toBe("dev");
    stubVersion("v1.0.0");
    await refreshDaemonVersion();
    expect(daemonVersion.get()).toBe("v1.0.0");
  });

  it("a refresh REPLACES the cached version (daemon restarted on a new build)", async () => {
    stubVersion("v1.0.0");
    await refreshDaemonVersion();
    expect(daemonVersion.get()).toBe("v1.0.0");

    stubVersion("v2.0.0");
    await refreshDaemonVersion();
    expect(daemonVersion.get()).toBe("v2.0.0");
  });

  it("notifies subscribers on refresh and falls back to 'dev' on failure", async () => {
    const seen: string[] = [];
    const unsub = daemonVersion.subscribe(() => seen.push(daemonVersion.get()));

    stubVersion("v3.0.0");
    await refreshDaemonVersion();
    expect(seen).toContain("v3.0.0");

    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new Error("daemon down");
      }),
    );
    await refreshDaemonVersion();
    expect(daemonVersion.get()).toBe("dev");
    unsub();
  });
});
