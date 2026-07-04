package store

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskpb "todoapp/gen/task"
)

func mustList(t *testing.T, s *Store, p Page) []*taskpb.Task {
	t.Helper()
	tasks, _, err := s.List(context.Background(), p)
	if err != nil {
		t.Fatalf("List(%+v): %v", p, err)
	}
	return tasks
}

func titlesOf(tasks []*taskpb.Task) []string {
	out := make([]string, len(tasks))
	for i, task := range tasks {
		out[i] = task.GetTitle()
	}
	return out
}

func assertTitles(t *testing.T, tasks []*taskpb.Task, want ...string) {
	t.Helper()
	if got := titlesOf(tasks); !slices.Equal(got, want) {
		t.Errorf("titles = %v, want %v", got, want)
	}
}

func TestListFilterLabels(t *testing.T) {
	s, _ := newTestStore(t)
	mustCreate(t, s, "A", "a")
	mustCreate(t, s, "B", "a", "b")
	mustCreate(t, s, "C", "b", "c")

	order := Page{OrderBy: "title"}
	tests := []struct {
		name   string
		filter *taskpb.TaskFilter
		want   []string
	}{
		{"any single", &taskpb.TaskFilter{LabelsAny: []string{"a"}}, []string{"A", "B"}},
		{"any multiple", &taskpb.TaskFilter{LabelsAny: []string{"a", "c"}}, []string{"A", "B", "C"}},
		{"any normalized", &taskpb.TaskFilter{LabelsAny: []string{" a "}}, []string{"A", "B"}},
		{"all", &taskpb.TaskFilter{LabelsAll: []string{"a", "b"}}, []string{"B"}},
		{"all single", &taskpb.TaskFilter{LabelsAll: []string{"b"}}, []string{"B", "C"}},
		{"any+all", &taskpb.TaskFilter{LabelsAny: []string{"a", "c"}, LabelsAll: []string{"b"}}, []string{"B", "C"}},
		{"no match", &taskpb.TaskFilter{LabelsAll: []string{"a", "c"}}, nil},
	}
	for _, tc := range tests {
		p := order
		p.Filter = tc.filter
		if got := titlesOf(mustList(t, s, p)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: titles = %v, want %v", tc.name, got, tc.want)
		}
	}

	_, _, err := s.List(context.Background(), Page{Filter: &taskpb.TaskFilter{LabelsAny: []string{" "}}})
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("blank filter label err = %v, want ErrInvalid", err)
	}
}

func TestListFilterCompleted(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	mustCreate(t, s, "active")
	done := mustCreate(t, s, "done")
	if _, err := s.Update(ctx, done.GetId(), 0, func(tk *taskpb.Task) error {
		tk.CompletedTime = msTs(1000)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	assertTitles(t, mustList(t, s, Page{OrderBy: "title"}), "active", "done")
	assertTitles(t, mustList(t, s, Page{OrderBy: "title", Filter: &taskpb.TaskFilter{Completed: proto.Bool(true)}}), "done")
	assertTitles(t, mustList(t, s, Page{OrderBy: "title", Filter: &taskpb.TaskFilter{Completed: proto.Bool(false)}}), "active")
}

func TestListFilterDueBounds(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	for _, tc := range []struct {
		title string
		dueMs int64
	}{{"t1", 1000}, {"t2", 2000}, {"t3", 3000}} {
		if _, err := s.Create(ctx, tc.title, "", nil, msTs(tc.dueMs)); err != nil {
			t.Fatal(err)
		}
	}
	mustCreate(t, s, "nodue")

	tests := []struct {
		name   string
		filter *taskpb.TaskFilter
		want   []string
	}{
		// Half-open: after is inclusive, before exclusive; both at exact task
		// timestamps. Tasks without a due date never match a bound.
		{"after inclusive", &taskpb.TaskFilter{DueAfter: msTs(2000)}, []string{"t2", "t3"}},
		{"before exclusive", &taskpb.TaskFilter{DueBefore: msTs(2000)}, []string{"t1"}},
		{"both", &taskpb.TaskFilter{DueAfter: msTs(1000), DueBefore: msTs(3000)}, []string{"t1", "t2"}},
		{"empty range", &taskpb.TaskFilter{DueAfter: msTs(2000), DueBefore: msTs(2000)}, nil},
	}
	for _, tc := range tests {
		got := titlesOf(mustList(t, s, Page{OrderBy: "title", Filter: tc.filter}))
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: titles = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestListFilterHasDue(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.Create(context.Background(), "with", "", nil, msTs(1000)); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, "without")

	assertTitles(t, mustList(t, s, Page{OrderBy: "title", Filter: &taskpb.TaskFilter{HasDue: proto.Bool(true)}}), "with")
	assertTitles(t, mustList(t, s, Page{OrderBy: "title", Filter: &taskpb.TaskFilter{HasDue: proto.Bool(false)}}), "without")
}

func TestListFilterSource(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	mustCreate(t, s, "local")
	if _, err := s.UpsertExternal(ctx, "github", []*taskpb.ExternalTask{
		{ExternalRef: "pr/1", Title: "external"},
	}, nil, false); err != nil {
		t.Fatal(err)
	}

	// Unset: any source. Set to "": local tasks only. Set: exact match.
	assertTitles(t, mustList(t, s, Page{OrderBy: "title"}), "external", "local")
	assertTitles(t, mustList(t, s, Page{OrderBy: "title", Filter: &taskpb.TaskFilter{Source: proto.String("")}}), "local")
	assertTitles(t, mustList(t, s, Page{OrderBy: "title", Filter: &taskpb.TaskFilter{Source: proto.String("github")}}), "external")
	assertTitles(t, mustList(t, s, Page{OrderBy: "title", Filter: &taskpb.TaskFilter{Source: proto.String("gitlab")}}))
}

func TestListFilterText(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "Ship the 100% release", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Ship the 1000 release", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "unrelated", "NOTES mention ShIp", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "a_b underscore", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "aXb not underscore", "", nil, nil); err != nil {
		t.Fatal(err)
	}

	find := func(text string) []string {
		return titlesOf(mustList(t, s, Page{OrderBy: "title", Filter: &taskpb.TaskFilter{Text: text}}))
	}
	// Case-insensitive, over title OR notes.
	if got := find("ship"); !slices.Equal(got, []string{"Ship the 100% release", "Ship the 1000 release", "unrelated"}) {
		t.Errorf("ship: %v", got)
	}
	// LIKE metacharacters in the needle are literal.
	if got := find("100%"); !slices.Equal(got, []string{"Ship the 100% release"}) {
		t.Errorf("100%%: %v", got)
	}
	if got := find("a_b"); !slices.Equal(got, []string{"a_b underscore"}) {
		t.Errorf("a_b: %v", got)
	}
	if got := find(`\`); len(got) != 0 {
		t.Errorf("backslash: %v, want none", got)
	}
}

func TestListOrdering(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	// Created in this order (clock steps 1s per write): banana, apple, cherry.
	// Dues: banana 2000, apple none, cherry 1000.
	if _, err := s.Create(ctx, "banana", "", nil, msTs(2000)); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, "apple")
	if _, err := s.Create(ctx, "cherry", "", nil, msTs(1000)); err != nil {
		t.Fatal(err)
	}
	// Touch banana so updated order differs from created order.
	banana := mustList(t, s, Page{Filter: &taskpb.TaskFilter{Text: "banana"}})[0]
	if _, err := s.Update(ctx, banana.GetId(), 0, func(tk *taskpb.Task) error { return nil }); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		orderBy string
		want    []string
	}{
		{"", []string{"cherry", "apple", "banana"}}, // default: created desc
		{"created", []string{"banana", "apple", "cherry"}},
		{"created asc", []string{"banana", "apple", "cherry"}},
		{"created desc", []string{"cherry", "apple", "banana"}},
		{"updated asc", []string{"apple", "cherry", "banana"}},
		{"updated desc", []string{"banana", "cherry", "apple"}},
		{"title", []string{"apple", "banana", "cherry"}},
		{"title desc", []string{"cherry", "banana", "apple"}},
		// Tasks without a due date sort last in BOTH directions.
		{"due", []string{"cherry", "banana", "apple"}},
		{"due asc", []string{"cherry", "banana", "apple"}},
		{"due desc", []string{"banana", "cherry", "apple"}},
	}
	for _, tc := range tests {
		if got := titlesOf(mustList(t, s, Page{OrderBy: tc.orderBy})); !slices.Equal(got, tc.want) {
			t.Errorf("order_by %q: titles = %v, want %v", tc.orderBy, got, tc.want)
		}
	}
}

func TestListOrderingTieBreakByID(t *testing.T) {
	s, clk := newTestStore(t)
	clk.freeze() // identical created_ms for all three tasks
	tasks := []*taskpb.Task{
		mustCreate(t, s, "x"),
		mustCreate(t, s, "y"),
		mustCreate(t, s, "z"),
	}
	ids := taskIDs(tasks)
	slices.Sort(ids)

	asc := taskIDs(mustList(t, s, Page{OrderBy: "created asc"}))
	if !slices.Equal(asc, ids) {
		t.Errorf("created asc ids = %v, want id-ascending %v", asc, ids)
	}
	desc := taskIDs(mustList(t, s, Page{OrderBy: "created desc"}))
	slices.Reverse(ids)
	if !slices.Equal(desc, ids) {
		t.Errorf("created desc ids = %v, want id-descending %v", desc, ids)
	}
}

func TestListInvalidOrderBy(t *testing.T) {
	s, _ := newTestStore(t)
	for _, orderBy := range []string{"priority", "created descending", "title asc extra", "asc", "due  DESC"} {
		if _, _, err := s.List(context.Background(), Page{OrderBy: orderBy}); !errors.Is(err, ErrInvalid) {
			t.Errorf("List(order_by=%q) err = %v, want ErrInvalid", orderBy, err)
		}
	}
}

func TestListPageSizeDefaults(t *testing.T) {
	s, _ := newTestStore(t)
	for i := 0; i < 3; i++ {
		mustCreate(t, s, "t")
	}
	tasks, next, err := s.List(context.Background(), Page{PageSize: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 || next != "" {
		t.Errorf("PageSize 0: got %d tasks, token %q; want all 3, no token", len(tasks), next)
	}
	// The cap is applied, not rejected.
	if _, _, err := s.List(context.Background(), Page{PageSize: 5000}); err != nil {
		t.Errorf("PageSize 5000: %v", err)
	}
}

// walkPages pages through the whole result set and asserts it equals the
// unpaginated listing: same order, no duplicates, no omissions.
func walkPages(t *testing.T, s *Store, p Page, wantMinPages int) {
	t.Helper()
	ctx := context.Background()
	full := mustList(t, s, Page{Filter: p.Filter, OrderBy: p.OrderBy})

	var walked []string
	pages := 0
	token := ""
	for {
		q := p
		q.PageToken = token
		tasks, next, err := s.List(ctx, q)
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		pages++
		walked = append(walked, taskIDs(tasks)...)
		if next == "" {
			break
		}
		if len(tasks) != int(p.PageSize) {
			t.Errorf("non-final page %d has %d tasks, want %d", pages, len(tasks), p.PageSize)
		}
		token = next
	}
	if pages < wantMinPages {
		t.Errorf("walked %d pages, want >= %d", pages, wantMinPages)
	}
	if !slices.Equal(walked, taskIDs(full)) {
		t.Errorf("paginated walk (%v) differs from unpaginated list (%v)", walked, taskIDs(full))
	}
}

func TestListPagination(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	// 10 tasks; every third has no due date, and due values repeat so the
	// keyset must fall through to the id tie-break.
	for i := 0; i < 10; i++ {
		var due *timestamppb.Timestamp
		if i%3 != 0 {
			due = msTs(int64(1000 * (i % 4)))
		}
		title := string(rune('a' + i%4)) // duplicate titles too
		if _, err := s.Create(ctx, title, "", nil, due); err != nil {
			t.Fatal(err)
		}
	}

	for _, orderBy := range []string{"", "created asc", "due asc", "due desc", "title desc", "updated"} {
		t.Run("order="+orderBy, func(t *testing.T) {
			walkPages(t, s, Page{OrderBy: orderBy, PageSize: 3}, 4)
		})
	}
}

func TestListPaginationWithFilter(t *testing.T) {
	s, _ := newTestStore(t)
	for i := 0; i < 7; i++ {
		mustCreate(t, s, "t", "keep")
	}
	mustCreate(t, s, "t", "other")
	walkPages(t, s, Page{Filter: &taskpb.TaskFilter{LabelsAll: []string{"keep"}}, OrderBy: "created asc", PageSize: 2}, 3)
}

func TestListTokenOrderMismatch(t *testing.T) {
	s, _ := newTestStore(t)
	for i := 0; i < 3; i++ {
		mustCreate(t, s, "t")
	}
	_, token, err := s.List(context.Background(), Page{OrderBy: "created desc", PageSize: 1})
	if err != nil || token == "" {
		t.Fatalf("first page: token %q, err %v", token, err)
	}
	// Same token under a different order.
	if _, _, err := s.List(context.Background(), Page{OrderBy: "title asc", PageSize: 1, PageToken: token}); !errors.Is(err, ErrInvalid) {
		t.Errorf("order mismatch err = %v, want ErrInvalid", err)
	}
	// The token remains valid for its own order.
	if _, _, err := s.List(context.Background(), Page{OrderBy: "created desc", PageSize: 1, PageToken: token}); err != nil {
		t.Errorf("token reuse under same order: %v", err)
	}
}

func TestListGarbageToken(t *testing.T) {
	s, _ := newTestStore(t)
	mustCreate(t, s, "t")
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	for _, token := range []string{
		"not base64 !!!",
		enc("not json"),
		enc(`{"o":42}`),
		enc(`{"o":"created desc","k":[1]}`),       // wrong key count
		enc(`{"o":"created desc","k":[1,2]}`),     // id key must be a string
		enc(`{"o":"created desc","k":["x","y"]}`), // ms key must be numeric
		enc(`{"o":"created desc","k":[1.5,"x"]}`), // non-integer ms key
	} {
		if _, _, err := s.List(context.Background(), Page{PageToken: token}); !errors.Is(err, ErrInvalid) {
			t.Errorf("token %q err = %v, want ErrInvalid", token, err)
		}
	}
}

func TestListLabels(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	mustCreate(t, s, "active", "a", "b")
	done := mustCreate(t, s, "done", "b", "c")
	if _, err := s.Update(ctx, done.GetId(), 0, func(tk *taskpb.Task) error {
		tk.CompletedTime = msTs(1000)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	assertLabelCounts := func(got []*taskpb.LabelCount, want map[string]int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("labels = %v, want %v", got, want)
			return
		}
		prev := ""
		for _, lc := range got {
			if lc.GetLabel() <= prev {
				t.Errorf("labels not sorted: %q after %q", lc.GetLabel(), prev)
			}
			prev = lc.GetLabel()
			if want[lc.GetLabel()] != lc.GetCount() {
				t.Errorf("count[%q] = %d, want %d", lc.GetLabel(), lc.GetCount(), want[lc.GetLabel()])
			}
		}
	}
	active, err := s.ListLabels(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	// Labels used solely by completed tasks are omitted.
	assertLabelCounts(active, map[string]int64{"a": 1, "b": 1})

	all, err := s.ListLabels(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	assertLabelCounts(all, map[string]int64{"a": 1, "b": 2, "c": 1})
}
