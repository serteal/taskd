package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

func newWatchCmd(a *app) *cobra.Command {
	var filter string
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Stream the live change feed (Ctrl-C to stop)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := &cliWatchHandler{
				out:  cmd.OutOrStdout(),
				errW: cmd.ErrOrStderr(),
				json: a.json,
			}
			return a.client.WatchItems(cmd.Context(), filter, 0, h)
		},
	}
	cmd.Flags().StringVarP(&filter, "filter", "f", "", "CEL filter on the event's after-image")
	return cmd
}

type cliWatchHandler struct {
	out  io.Writer
	errW io.Writer
	json bool
}

func (h *cliWatchHandler) HandleEvent(ev *taskcorev1.Event) error {
	if h.json {
		return printProto(h.out, ev)
	}
	typ := strings.TrimPrefix(ev.GetType().String(), "CHANGE_TYPE_")
	_, err := fmt.Fprintf(h.out, "%s %s %s %s\n",
		time.Now().Format("15:04:05"), typ, shortID(ev.GetItemId()), itemTitle(ev.GetItem()))
	return err
}

// HandleResync drains the snapshot (a live tail has no state to rebuild)
// and notes the discontinuity once on stderr.
func (h *cliWatchHandler) HandleResync(replay func(fn func(*taskcorev1.Item) error) error) (uint64, error) {
	fmt.Fprintln(h.errW, "watch: cursor expired; resynced to the current state (events may have been missed)")
	if err := replay(func(*taskcorev1.Item) error { return nil }); err != nil {
		return 0, err
	}
	return 0, nil // resume from the snapshot cursor
}

func (h *cliWatchHandler) HandleCheckpoint(uint64) error { return nil }
