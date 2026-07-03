package main

import (
	"fmt"
	"os"
	"strings"
)

// destructiveIntents is the default-refused set for agent callers. DESIGN §14:
// MCP callers are a distinct principal class with no confirm dialog, so
// destructive (and confirm-gated) intents are refused by default. "delete" is
// the only such standard intent today; future destructive names added here are
// refused by construction until explicitly allowlisted.
var destructiveIntents = map[string]bool{
	"delete": true,
}

// guardIntent enforces the agent guardrail client-side, before any RPC: a
// destructive intent is refused unless the user opted in by naming it in the
// TASKMCP_ALLOW_DESTRUCTIVE env var (comma-separated allowlist, e.g. "delete").
// Refusing here — not at the daemon — is deliberate: the daemon cannot tell an
// agent from a human, so the principal-class policy lives in the agent
// frontend. A nil return means the intent may proceed.
func guardIntent(intent string) error {
	name := strings.ToLower(strings.TrimSpace(intent))
	if !destructiveIntents[name] {
		return nil
	}
	if destructiveAllowed(name) {
		return nil
	}
	return fmt.Errorf(
		"refused: %q is a destructive action, and agents (MCP callers) have no confirm dialog, so destructive actions are blocked by default (DESIGN §14). "+
			"To permit it, set the environment variable TASKMCP_ALLOW_DESTRUCTIVE to a comma-separated allowlist that includes %q (e.g. TASKMCP_ALLOW_DESTRUCTIVE=%s).",
		name, name, name)
}

// destructiveAllowed reports whether name appears in the caller's
// TASKMCP_ALLOW_DESTRUCTIVE allowlist. Read at call time so per-process env
// changes (and tests) take effect without a restart.
func destructiveAllowed(name string) bool {
	for _, entry := range strings.Split(os.Getenv("TASKMCP_ALLOW_DESTRUCTIVE"), ",") {
		if strings.EqualFold(strings.TrimSpace(entry), name) {
			return true
		}
	}
	return false
}
