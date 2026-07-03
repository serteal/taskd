package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/daemon"
	"todoapp/pkg/taskclient"
)

func newDaemonCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run and manage the taskd daemon",
		// Daemon commands manage the daemon instead of assuming it; this
		// no-op overrides the root's dial-on-start hook for the whole group.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	}
	cmd.AddCommand(
		newDaemonRunCmd(a),
		newDaemonStartCmd(a),
		newDaemonStatusCmd(a),
		newDaemonStopCmd(a),
	)
	return cmd
}

func newDaemonRunCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run taskd in the foreground (Ctrl-C to stop)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return daemon.Run(cmd.Context(), daemon.Config{Dir: a.dir})
		},
	}
}

func newDaemonStartCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start taskd detached, logging to <dir>/taskd.log",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if resp, err := pingDaemon(cmd.Context(), a.dir); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "daemon already running (version %s)\n", resp.GetDaemonVersion())
				return nil
			}
			if err := os.MkdirAll(a.dir, 0o700); err != nil {
				return err
			}
			logPath := filepath.Join(a.dir, "taskd.log")
			logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			defer logf.Close()
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			child := exec.Command(exe, "daemon", "run", "--dir", a.dir)
			child.Stdout = logf
			child.Stderr = logf
			child.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // survive our terminal
			if err := child.Start(); err != nil {
				return err
			}
			// Reap on failure only; on success the daemon outlives us.
			sock := daemon.SocketPath(a.dir)
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(sock); err == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "taskd started (pid %d)\n", child.Process.Pid)
					_ = child.Process.Release()
					return nil
				}
				time.Sleep(50 * time.Millisecond)
			}
			return fmt.Errorf("taskd did not create %s within 5s; check %s", sock, logPath)
		},
	}
}

func newDaemonStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report whether taskd is running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := pingDaemon(cmd.Context(), a.dir)
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "not running")
				return nil
			}
			if a.json {
				return printProto(cmd.OutOrStdout(), resp)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "running: version %s, event cursors [%d, %d]\n",
				resp.GetDaemonVersion(), resp.GetOldestCursor(), resp.GetLatestCursor())
			return nil
		},
	}
}

func newDaemonStopCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop a detached taskd (SIGTERM via the pidfile)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := os.ReadFile(daemon.PidPath(a.dir))
			if err != nil {
				return fmt.Errorf("not running? no pidfile at %s", daemon.PidPath(a.dir))
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil {
				return fmt.Errorf("malformed pidfile: %w", err)
			}
			proc, err := os.FindProcess(pid)
			if err != nil {
				return err
			}
			if err := proc.Signal(syscall.SIGTERM); err != nil {
				return fmt.Errorf("signal pid %d: %w", pid, err)
			}
			sock := daemon.SocketPath(a.dir)
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(sock); err != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "stopped (pid %d)\n", pid)
					return nil
				}
				time.Sleep(50 * time.Millisecond)
			}
			return fmt.Errorf("sent SIGTERM to pid %d but %s still exists after 5s", pid, sock)
		},
	}
}

// pingDaemon dials the socket and pings with a short timeout.
func pingDaemon(ctx context.Context, dir string) (*taskcorev1.PingResponse, error) {
	cl, err := taskclient.Dial(ctx, daemon.SocketPath(dir), clientName)
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return cl.Ping(pctx)
}
