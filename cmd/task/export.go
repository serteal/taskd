package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/server"
)

// exportVersion is the JSONL format version; import refuses others.
const exportVersion = 1

// exportLine is one JSONL line: a header, an item, or a view.
type exportLine struct {
	Type       string          `json:"type"`
	Version    int             `json:"version,omitempty"`
	ExportedAt string          `json:"exported_at,omitempty"`
	Item       json.RawMessage `json:"item,omitempty"`
	View       json.RawMessage `json:"view,omitempty"`
}

func newExportCmd(a *app) *cobra.Command {
	var outPath string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export all items and views as JSONL (portability and paranoia)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var w io.Writer = cmd.OutOrStdout()
			if outPath != "" {
				f, err := os.Create(outPath)
				if err != nil {
					return err
				}
				defer f.Close()
				w = f
			}
			bw := bufio.NewWriter(w)
			enc := json.NewEncoder(bw)

			if err := enc.Encode(exportLine{
				Type:       "header",
				Version:    exportVersion,
				ExportedAt: time.Now().UTC().Format(time.RFC3339),
			}); err != nil {
				return err
			}

			ctx := cmd.Context()
			writeProto := func(typ string, m proto.Message) error {
				raw, err := protojson.Marshal(m)
				if err != nil {
					return err
				}
				line := exportLine{Type: typ}
				switch typ {
				case "item":
					line.Item = raw
				case "view":
					line.View = raw
				}
				return enc.Encode(line)
			}

			// ALL items: the archive is part of the export.
			if _, err := a.client.QueryAll(ctx, "", "", func(it *taskcorev1.Item) error {
				return writeProto("item", it)
			}); err != nil {
				return err
			}
			views, err := a.client.Views().ListViews(ctx, &taskcorev1.ListViewsRequest{})
			if err != nil {
				return err
			}
			for _, v := range views.GetViews() {
				if err := writeProto("view", v); err != nil {
					return err
				}
			}
			if err := bw.Flush(); err != nil {
				return err
			}
			if outPath != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "exported to %s\n", outPath)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&outPath, "output", "o", "", "write to FILE instead of stdout")
	return cmd
}

func newImportCmd(a *app) *cobra.Command {
	var inPath string
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import a JSONL export: native tasks are recreated (new ids), views upserted",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var r io.Reader = cmd.InOrStdin()
			if inPath != "" {
				f, err := os.Open(inPath)
				if err != nil {
					return err
				}
				defer f.Close()
				r = f
			}
			sc := bufio.NewScanner(r)
			sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

			ctx := cmd.Context()
			errW := cmd.ErrOrStderr()
			unmarshal := protojson.UnmarshalOptions{DiscardUnknown: true}
			var (
				sawHeader      bool
				itemCount      int
				viewCount      int
				skippedMirrors int
			)
			for lineNo := 1; sc.Scan(); lineNo++ {
				if len(sc.Bytes()) == 0 {
					continue
				}
				var line exportLine
				if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
					return fmt.Errorf("line %d: %w", lineNo, err)
				}
				if !sawHeader {
					if line.Type != "header" {
						return fmt.Errorf("line %d: first line must be the export header", lineNo)
					}
					if line.Version != exportVersion {
						return fmt.Errorf("unsupported export version %d (this build reads version %d)", line.Version, exportVersion)
					}
					sawHeader = true
					continue
				}
				switch line.Type {
				case "item":
					it := &taskcorev1.Item{}
					if err := unmarshal.Unmarshal(line.Item, it); err != nil {
						return fmt.Errorf("line %d: %w", lineNo, err)
					}
					// Mirrored items belong to their connectors: recreating
					// them here would forge remote-confirmed truth.
					if it.GetKind() != server.NativeKind || it.GetMirror() != nil {
						skippedMirrors++
						fmt.Fprintf(errW, "warning: skipping mirrored item %s (%s): it belongs to connector %q\n",
							shortID(it.GetId()), itemTitle(it), it.GetMirror().GetLink().GetConnectorInstance())
						continue
					}
					if len(it.GetRelations()) > 0 {
						fmt.Fprintf(errW, "warning: dropping %d relation(s) on %s: relations belong to connectors\n",
							len(it.GetRelations()), shortID(it.GetId()))
					}
					// The whole todo survives, completion included; the core
					// assigns a new id.
					if _, err := a.client.Items().CreateItem(ctx, &taskcorev1.CreateItemRequest{Todo: it.GetTodo()}); err != nil {
						return fmt.Errorf("line %d (%s): %w", lineNo, itemTitle(it), err)
					}
					itemCount++
				case "view":
					v := &taskcorev1.View{}
					if err := unmarshal.Unmarshal(line.View, v); err != nil {
						return fmt.Errorf("line %d: %w", lineNo, err)
					}
					if _, err := a.client.Views().SaveView(ctx, &taskcorev1.SaveViewRequest{View: v}); err != nil {
						return fmt.Errorf("line %d (view %s): %w", lineNo, v.GetName(), err)
					}
					viewCount++
				default:
					fmt.Fprintf(errW, "warning: line %d: unknown record type %q skipped\n", lineNo, line.Type)
				}
			}
			if err := sc.Err(); err != nil {
				return err
			}
			if !sawHeader {
				return fmt.Errorf("empty input: no export header found")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "imported %d items (new ids assigned), %d views; skipped %d mirrored items\n",
				itemCount, viewCount, skippedMirrors)
			return nil
		},
	}
	cmd.Flags().StringVarP(&inPath, "input", "i", "", "read from FILE instead of stdin")
	return cmd
}
