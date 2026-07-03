package main

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/daemon"
)

// withIntents dials a dedicated connection for IntentService calls. The
// taskclient SDK does not expose IntentService yet; the root command's dial
// already verified daemon liveness, so this second (lazy) connection is
// plumbing, not a second handshake (mirrors withRules).
func withIntents(cmd *cobra.Command, a *app, fn func(ctx context.Context, ic taskcorev1.IntentServiceClient) error) error {
	conn, err := grpc.NewClient("unix://"+daemon.SocketPath(a.dir),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			return invoker(metadata.AppendToOutgoingContext(ctx, "x-task-client", clientName), method, req, reply, cc, opts...)
		}),
	)
	if err != nil {
		return err
	}
	defer conn.Close()
	return fn(cmd.Context(), taskcorev1.NewIntentServiceClient(conn))
}

// intentStateName renders IntentState for tables: the bare state word without
// the wire enum's INTENT_STATE_ prefix (QUEUED, CONFIRMED, ...).
func intentStateName(s taskcorev1.IntentState) string {
	return strings.TrimPrefix(s.String(), "INTENT_STATE_")
}

// humanAge renders how long ago ts was, coarsely: "5s", "3m", "2h", "4d".
func humanAge(ts *timestamppb.Timestamp, now time.Time) string {
	if ts == nil {
		return ""
	}
	d := now.Sub(ts.AsTime())
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// newPendingCmd renders the outbox. The default view is the live outbox
// (QUEUED, INFLIGHT, FAILED — what still needs attention); --all folds in the
// terminal CONFIRMED and DISCARDED history.
func newPendingCmd(a *app) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "pending",
		Short: "The outbox: intents queued, in flight, or failed on their way to remotes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var states []taskcorev1.IntentState
			if all {
				// Explicit five-state filter — the store's empty default is the
				// live outbox only, so --all must name every state.
				states = []taskcorev1.IntentState{
					taskcorev1.IntentState_INTENT_STATE_QUEUED,
					taskcorev1.IntentState_INTENT_STATE_INFLIGHT,
					taskcorev1.IntentState_INTENT_STATE_CONFIRMED,
					taskcorev1.IntentState_INTENT_STATE_FAILED,
					taskcorev1.IntentState_INTENT_STATE_DISCARDED,
				}
			}
			return withIntents(cmd, a, func(ctx context.Context, ic taskcorev1.IntentServiceClient) error {
				resp, err := ic.ListIntents(ctx, &taskcorev1.ListIntentsRequest{States: states})
				if err != nil {
					return err
				}
				recs := resp.GetRecords()
				if a.json {
					for _, r := range recs {
						if err := printProto(cmd.OutOrStdout(), r); err != nil {
							return err
						}
					}
					return nil
				}
				if len(recs) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "outbox is empty")
					return nil
				}
				now := time.Now()
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
				// The full intent id is shown, not a prefix: `task retry` needs it.
				fmt.Fprintln(w, "ID\tITEM\tINTENT\tSTATE\tATTEMPTS\tAGE\tERROR")
				for _, r := range recs {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
						r.GetId(), shortID(r.GetItemId()), r.GetIntent(),
						intentStateName(r.GetState()), r.GetAttempts(),
						humanAge(r.GetCreatedAt(), now), truncateExpr(r.GetLastError(), 50))
				}
				return w.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include terminal CONFIRMED and DISCARDED intents")
	return cmd
}

func newRetryCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "retry <intent-id>",
		Short: "Re-queue a FAILED intent for delivery",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withIntents(cmd, a, func(ctx context.Context, ic taskcorev1.IntentServiceClient) error {
				resp, err := ic.RetryIntent(ctx, &taskcorev1.RetryIntentRequest{Id: args[0]})
				if err != nil {
					return err
				}
				r := resp.GetRecord()
				if a.json {
					return printProto(cmd.OutOrStdout(), r)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "retrying %s (%s on %s)\n", r.GetId(), r.GetIntent(), shortID(r.GetItemId()))
				return nil
			})
		},
	}
}

func newDiscardCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "discard <intent-id>",
		Short: "Terminally drop a QUEUED or FAILED intent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withIntents(cmd, a, func(ctx context.Context, ic taskcorev1.IntentServiceClient) error {
				resp, err := ic.DiscardIntent(ctx, &taskcorev1.DiscardIntentRequest{Id: args[0]})
				if err != nil {
					return err
				}
				r := resp.GetRecord()
				if a.json {
					return printProto(cmd.OutOrStdout(), r)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "discarded %s\n", r.GetId())
				return nil
			})
		},
	}
}

// newRenameCmd is the showcase of one-write-API routing. A native task's title
// is a local override; a tracked item's title routes to the connector as a
// rename intent, because the mirror only ever holds remote-confirmed truth.
func newRenameCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <id> <new title>",
		Short: "Rename an item (locally for native tasks, via the connector for tracked ones)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			title := strings.Join(args[1:], " ")
			ctx := cmd.Context()
			items := a.client.Items()
			cur, err := items.GetItem(ctx, &taskcorev1.GetItemRequest{Id: args[0]})
			if err != nil {
				return err
			}
			it := cur.GetItem()

			if it.GetMirror() == nil {
				resp, err := items.UpdateItem(ctx, &taskcorev1.UpdateItemRequest{
					Id:         it.GetId(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"todo.title_override"}},
					Item:       &taskcorev1.Item{Todo: &taskcorev1.Todo{TitleOverride: title}},
				})
				if err != nil {
					return err
				}
				if a.json {
					return printProto(cmd.OutOrStdout(), resp.GetItem())
				}
				fmt.Fprintf(cmd.OutOrStdout(), "renamed %s %s\n", shortID(resp.GetItem().GetId()), title)
				return nil
			}

			// Mirror-backed title: UpdateItem routes it through the outbox and
			// returns the intent record alongside the (possibly updated) item.
			resp, err := items.UpdateItem(ctx, &taskcorev1.UpdateItemRequest{
				Id:         it.GetId(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"mirror.title"}},
				Item:       &taskcorev1.Item{Mirror: &taskcorev1.Mirror{Title: title}},
			})
			if err != nil {
				return err
			}
			if a.json {
				return printProto(cmd.OutOrStdout(), resp.GetItem())
			}
			short := shortID(it.GetId())
			rec := resp.GetIntent()
			switch rec.GetState() {
			case taskcorev1.IntentState_INTENT_STATE_CONFIRMED:
				fmt.Fprintf(cmd.OutOrStdout(), "renamed %s %s (remote confirmed)\n", short, title)
			case taskcorev1.IntentState_INTENT_STATE_FAILED:
				return fmt.Errorf("rename failed: %s", rec.GetLastError())
			default: // QUEUED / INFLIGHT: durable, the outbox delivers when it can
				fmt.Fprintf(cmd.OutOrStdout(), "rename queued for delivery (intent %s); see task pending\n", rec.GetId())
			}
			return nil
		},
	}
}

// newLinkCmd attaches a native task to a remote object (LinkItem): the one
// sanctioned kind change in the system. Server errors are surfaced verbatim —
// they already say precisely what went wrong.
func newLinkCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "link <id> <instance> <ref>",
		Short: "Attach a native task to a remote object via a connector instance",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := a.client.Items().LinkItem(cmd.Context(), &taskcorev1.LinkItemRequest{
				Id:                args[0],
				ConnectorInstance: args[1],
				Ref:               args[2],
			})
			if err != nil {
				return err
			}
			it := resp.GetItem()
			if a.json {
				return printProto(cmd.OutOrStdout(), it)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "linked %s -> %s %s via %s\n",
				shortID(it.GetId()), it.GetKind(), it.GetMirror().GetLink().GetExternalId(), args[1])
			return nil
		},
	}
}
