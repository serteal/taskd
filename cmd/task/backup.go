package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

func newBackupCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "backup <dest>",
		Short: "Online backup of the database (safe against a live WAL db; cron-able)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The daemon interprets the path, so resolve it against OUR
			// working directory first.
			dest, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			resp, err := a.client.Admin().Backup(cmd.Context(), &taskcorev1.BackupRequest{DestinationPath: dest})
			if err != nil {
				return err
			}
			if a.json {
				return printProto(cmd.OutOrStdout(), resp)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "backup written: %s (%d bytes)\n", dest, resp.GetBytesWritten())
			return nil
		},
	}
}
