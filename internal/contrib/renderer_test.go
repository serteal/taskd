package contrib_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
	viewv1 "todoapp/gen/taskcore/view/v1"
	"todoapp/internal/contrib"
	"todoapp/internal/query"
)

func TestRender(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	qe, err := query.NewEngine(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	r := contrib.NewRenderer(qe)

	// A plugin kind contributes a column and a badge; broken ones refuse.
	if err := r.Register(&viewv1.Presentation{
		Kind:    "gh.pr",
		Columns: []*viewv1.ColumnDef{{Id: "external", Label: "REMOTE", Expr: `item.mirror.link.external_id`}},
		Badges:  []*viewv1.BadgeRule{{When: `state == "merged"`, Label: "merged", Tone: viewv1.Tone_TONE_SUCCESS}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(&viewv1.Presentation{
		Kind:    "bad.kind",
		Columns: []*viewv1.ColumnDef{{Id: "x", Expr: `not valid ((`}},
	}); err == nil {
		t.Fatal("broken column expression must refuse registration")
	}

	native := &taskcorev1.Item{
		Id: "01A", Kind: "task",
		Todo: &taskcorev1.Todo{TitleOverride: "native thing", Project: "p",
			Labels: []string{"a", "b"}, Due: timestamppb.New(now.Add(48 * time.Hour))},
		CreatedAt: timestamppb.New(now.Add(-time.Hour)),
	}
	pr := &taskcorev1.Item{
		Id: "01B", Kind: "gh.pr",
		Mirror: &taskcorev1.Mirror{Title: "the PR", State: "merged",
			Link: &taskcorev1.ExternalLink{ConnectorInstance: "gh@t", ExternalId: "pr-7"}},
	}

	cols := []string{"title", "due", "external", "state"}
	rows := r.Render([]*taskcorev1.Item{native, pr}, cols, now)
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}

	// Native: title from override, due is the effective due as a datetime,
	// contributed column empty (unknown for this kind).
	n := rows[0]
	if n.Cells[0].GetText() != "native thing" {
		t.Fatalf("title cell = %v", n.Cells[0])
	}
	if dt := n.Cells[1].GetDatetime(); dt == nil || !dt.AsTime().Equal(now.Add(48*time.Hour)) {
		t.Fatalf("due cell = %v, want server-computed effective due", n.Cells[1])
	}
	if n.Cells[2].GetText() != "" || len(n.Badges) != 0 {
		t.Fatalf("native row leaked kind contributions: %v %v", n.Cells[2], n.Badges)
	}

	// PR: mirror title, contributed column evaluated, badge fired.
	p := rows[1]
	if p.Cells[0].GetText() != "the PR" || p.Cells[2].GetText() != "pr-7" || p.Cells[3].GetText() != "merged" {
		t.Fatalf("pr cells = %v", p.Cells)
	}
	if len(p.Badges) != 1 || p.Badges[0].GetLabel() != "merged" || p.Badges[0].GetTone() != viewv1.Tone_TONE_SUCCESS {
		t.Fatalf("pr badges = %v", p.Badges)
	}

	// Column evaluation errors render empty, never break the listing.
	if err := r.Register(&viewv1.Presentation{
		Kind:    "gh.pr",
		Columns: []*viewv1.ColumnDef{{Id: "external", Expr: `item.mirror.data["nope"].x`}},
	}); err != nil {
		t.Fatal(err)
	}
	rows = r.Render([]*taskcorev1.Item{pr}, []string{"external"}, now)
	if rows[0].Cells[0].GetText() != "" {
		t.Fatalf("error cell = %v, want empty text", rows[0].Cells[0])
	}

	if h := r.Header([]string{"title", "external"}); h[0] != "TITLE" || h[1] != "EXTERNAL" {
		t.Fatalf("header = %v", h)
	}
}
