// Package contrib is the contribution registry and the core-side renderer:
// the single place display expressions are evaluated. Frontends request
// rendered rows and map DisplayValues to their medium — they never embed
// CEL and never talk to plugins (DESIGN.md §10).
package contrib

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	taskcorev1 "todoapp/gen/taskcore/v1"
	viewv1 "todoapp/gen/taskcore/view/v1"
	"todoapp/internal/query"
)

// DefaultColumns is the consumer default when a RenderSpec has none.
var DefaultColumns = []string{"id", "title", "project", "labels", "due"}

// builtinColumns are computed in Go for every kind; kind contributions may
// add columns or override these by id for their items.
var builtinColumns = map[string]string{
	"id": "ID", "title": "TITLE", "project": "PROJECT", "labels": "LABELS",
	"due": "DUE", "state": "STATE", "kind": "KIND", "age": "AGE",
}

type Renderer struct {
	qe *query.Engine

	mu    sync.RWMutex
	kinds map[string]*compiled
}

type compiled struct {
	pres    *viewv1.Presentation
	columns map[string]func(*taskcorev1.Item) (bool, *viewv1.DisplayValue) // id -> eval
	badges  []func(*taskcorev1.Item) *viewv1.Badge
}

func NewRenderer(qe *query.Engine) *Renderer {
	return &Renderer{qe: qe, kinds: make(map[string]*compiled)}
}

// Register validates and compiles one kind's presentation; broken
// contributions are refused at plugin registration, never discovered by a
// frontend. Re-registering a kind replaces it.
func (r *Renderer) Register(p *viewv1.Presentation) error {
	if p.GetKind() == "" {
		return fmt.Errorf("contrib: presentation has no kind")
	}
	c := &compiled{
		pres:    p,
		columns: make(map[string]func(*taskcorev1.Item) (bool, *viewv1.DisplayValue)),
	}
	for _, col := range p.GetColumns() {
		if col.GetId() == "" || col.GetExpr() == "" {
			return fmt.Errorf("contrib: kind %q: column needs id and expr", p.GetKind())
		}
		m, err := r.qe.EvalProgram(col.GetExpr())
		if err != nil {
			return fmt.Errorf("contrib: kind %q column %q: %w", p.GetKind(), col.GetId(), err)
		}
		c.columns[col.GetId()] = func(it *taskcorev1.Item) (bool, *viewv1.DisplayValue) {
			out, err := m(it)
			if err != nil {
				return true, &viewv1.DisplayValue{Value: &viewv1.DisplayValue_Text{Text: ""}}
			}
			return true, toDisplay(out)
		}
	}
	for _, b := range p.GetBadges() {
		match, err := r.qe.Matcher(b.GetWhen())
		if err != nil {
			return fmt.Errorf("contrib: kind %q badge %q: %w", p.GetKind(), b.GetLabel(), err)
		}
		label, tone := b.GetLabel(), b.GetTone()
		c.badges = append(c.badges, func(it *taskcorev1.Item) *viewv1.Badge {
			if ok, err := match(it); err == nil && ok {
				return &viewv1.Badge{Label: label, Tone: tone}
			}
			return nil
		})
	}
	r.mu.Lock()
	r.kinds[p.GetKind()] = c
	r.mu.Unlock()
	return nil
}

// Presentations returns the merged registry, stable order.
func (r *Renderer) Presentations() []*viewv1.Presentation {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*viewv1.Presentation, 0, len(r.kinds))
	for _, c := range r.kinds {
		out = append(out, c.pres)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetKind() < out[j].GetKind() })
	return out
}

// Columns resolves the effective column list: empty input means the default.
func Columns(spec *viewv1.RenderSpec) []string {
	if cols := spec.GetColumns(); len(cols) > 0 {
		return cols
	}
	return DefaultColumns
}

// Render produces one row per item, cells aligned with cols.
func (r *Renderer) Render(items []*taskcorev1.Item, cols []string, now time.Time) []*viewv1.RenderedRow {
	rows := make([]*viewv1.RenderedRow, 0, len(items))
	for _, it := range items {
		r.mu.RLock()
		c := r.kinds[it.GetKind()]
		r.mu.RUnlock()
		row := &viewv1.RenderedRow{ItemId: it.GetId()}
		for _, col := range cols {
			if c != nil {
				if eval, ok := c.columns[col]; ok {
					_, v := eval(it)
					row.Cells = append(row.Cells, v)
					continue
				}
			}
			row.Cells = append(row.Cells, r.builtin(col, it, now))
		}
		if c != nil {
			for _, b := range c.badges {
				if badge := b(it); badge != nil {
					row.Badges = append(row.Badges, badge)
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// builtin renders the core-computed columns — including the effective due
// (override-wins through facet bindings), which is exactly what CLI-side
// rendering could never do for plugin kinds.
func (r *Renderer) builtin(col string, it *taskcorev1.Item, now time.Time) *viewv1.DisplayValue {
	text := func(s string) *viewv1.DisplayValue {
		return &viewv1.DisplayValue{Value: &viewv1.DisplayValue_Text{Text: s}}
	}
	switch col {
	case "id":
		return text(it.GetId())
	case "title":
		t := it.GetTodo().GetTitleOverride()
		if t == "" {
			t = it.GetMirror().GetTitle()
		}
		return text(t)
	case "project":
		return text(it.GetTodo().GetProject())
	case "labels":
		return text(strings.Join(it.GetTodo().GetLabels(), ","))
	case "due":
		if d := r.qe.EffectiveDue(it); d != nil {
			return &viewv1.DisplayValue{Value: &viewv1.DisplayValue_Datetime{Datetime: timestamppb.New(*d)}}
		}
		return text("")
	case "state":
		return text(it.GetMirror().GetState())
	case "kind":
		return text(it.GetKind())
	case "age":
		if c := it.GetCreatedAt(); c != nil {
			return &viewv1.DisplayValue{Value: &viewv1.DisplayValue_Duration{Duration: durationpb.New(now.Sub(c.AsTime()))}}
		}
		return text("")
	default:
		return text("") // unknown column ids render empty, never error a listing
	}
}

// Header returns display labels for cols (kind labels can't apply globally,
// so contributed columns fall back to upper-cased ids).
func (r *Renderer) Header(cols []string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		if l, ok := builtinColumns[c]; ok {
			out[i] = l
		} else {
			out[i] = strings.ToUpper(c)
		}
	}
	return out
}

// toDisplay maps a CEL result to the display vocabulary by type.
func toDisplay(v any) *viewv1.DisplayValue {
	switch x := v.(type) {
	case string:
		return &viewv1.DisplayValue{Value: &viewv1.DisplayValue_Text{Text: x}}
	case bool:
		s := ""
		if x {
			s = "✓"
		}
		return &viewv1.DisplayValue{Value: &viewv1.DisplayValue_Text{Text: s}}
	case int64:
		return &viewv1.DisplayValue{Value: &viewv1.DisplayValue_Count{Count: x}}
	case uint64:
		return &viewv1.DisplayValue{Value: &viewv1.DisplayValue_Count{Count: int64(x)}}
	case float64:
		return &viewv1.DisplayValue{Value: &viewv1.DisplayValue_Text{Text: fmt.Sprintf("%g", x)}}
	case time.Time:
		return &viewv1.DisplayValue{Value: &viewv1.DisplayValue_Datetime{Datetime: timestamppb.New(x)}}
	case time.Duration:
		return &viewv1.DisplayValue{Value: &viewv1.DisplayValue_Duration{Duration: durationpb.New(x)}}
	case *timestamppb.Timestamp:
		return &viewv1.DisplayValue{Value: &viewv1.DisplayValue_Datetime{Datetime: x}}
	default:
		return &viewv1.DisplayValue{Value: &viewv1.DisplayValue_Text{Text: fmt.Sprintf("%v", v)}}
	}
}
