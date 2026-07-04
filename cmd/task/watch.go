package main

import (
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	taskpb "github.com/serteal/taskd/gen/task"
)

func newWatchCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Stream task changes until the stream ends",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stream, err := a.client().WatchTasks(cmd.Context(), connect.NewRequest(&taskpb.WatchTasksRequest{}))
			if err != nil {
				return err
			}
			defer stream.Close()
			for stream.Receive() {
				ev := stream.Msg()
				ts := time.Now().Format("15:04:05")
				switch c := ev.GetChange().(type) {
				case *taskpb.WatchTasksResponse_Task:
					sym := "~"
					if c.Task.GetRevision() == 1 {
						sym = "+"
					}
					fmt.Fprintf(a.out, "%s %s %s %s\n", ts, sym, shortID(c.Task.GetId()), c.Task.GetTitle())
				case *taskpb.WatchTasksResponse_DeletedTaskId:
					fmt.Fprintf(a.out, "%s - %s\n", ts, shortID(c.DeletedTaskId))
				default:
					// The opening handshake, or a change kind newer than this
					// binary: skip silently per the watch protocol.
				}
			}
			return stream.Err()
		},
	}
}
