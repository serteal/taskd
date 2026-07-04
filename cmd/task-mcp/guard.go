package main

import (
	"errors"
	"os"
)

// allowDestructiveEnv is the opt-in for permanent deletion. Agents (MCP
// callers) are a distinct principal class with no confirm dialog, so the
// guard lives here in the agent frontend — the daemon cannot tell an agent
// from a human.
const allowDestructiveEnv = "TASKMCP_ALLOW_DESTRUCTIVE"

// guardDelete enforces the destructive-action guard client-side, before any
// RPC: a refusal must never reach the daemon. A nil return means deletion
// may proceed.
func guardDelete() error {
	if destructiveAllowed() {
		return nil
	}
	return errors.New("refused: delete_task permanently and irreversibly deletes a task, and agents have no confirm dialog, so deletion is disabled by default. " +
		"Marking the task done with complete_task is usually what is wanted instead. " +
		"To enable deletion, start the task-mcp process with the environment variable " + allowDestructiveEnv + "=1.")
}

// destructiveAllowed reports whether the user opted in to deletion. Read at
// call time so per-process env changes (and tests) take effect without a
// restart.
func destructiveAllowed() bool {
	return os.Getenv(allowDestructiveEnv) == "1"
}
