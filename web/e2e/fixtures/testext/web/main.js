// A hand-written test-fixture extension (no build step). It deliberately
// exercises the failure paths so the suite can prove the host isolates them:
// a DetailSection that throws (error boundary), a command that throws
// (palette catch), plus a healthy presenter/command/token to confirm the
// happy paths. It matches tasks with source "test" (seeded via the API).

export default {
  name: "testext",
  register(api) {
    api.registerPresenter({
      match: (t) => t.source === "test",
      rowMeta: () => ({ subtitle: "TEST", icon: api.icon("diamond") }),
      // Throws on render → must be caught by the host's ExtensionBoundary.
      DetailSection: () => {
        throw new Error("testext: detail boom");
      },
    });
    api.registerCommand({
      id: "ok",
      title: "Test: say ok",
      group: "Test",
      run: () => api.notify.toast({ message: "test ok" }),
    });
    api.registerCommand({
      id: "boom",
      title: "Test: throw",
      group: "Test",
      // Throws when run → must be caught by the palette, not crash the app.
      run: () => {
        throw new Error("testext: command boom");
      },
    });
    api.registerQuickAddToken({
      hint: "urgent",
      match: (tok) => (tok === "urgent" ? { labels: ["urgent"] } : null),
    });
  },
};
