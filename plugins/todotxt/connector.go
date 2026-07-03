package todotxt

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	taskcorev1 "todoapp/gen/taskcore/v1"
	todotxtpluginv1 "todoapp/gen/todotxtplugin/v1"
	"todoapp/pkg/taskplugin"
)

// Manifest is the todotxt plugin's static self-description. The single kind
// todotxt.task is completable (set_completed writes the file's x/completion
// prefix) and schedulable (its due binds to the parsed due: tag), so
// `due < now + duration("24h")` works over mirrored tasks with zero core
// changes.
func Manifest() *pluginv1.Manifest {
	types := taskplugin.DescriptorSet(&todotxtpluginv1.Task{})
	return &pluginv1.Manifest{
		Name:     "todotxt",
		Version:  "0.1.0",
		Services: []string{"connector"},
		Kinds: []*pluginv1.KindRegistration{
			{
				Kind: KindTask,
				Facets: []*taskcorev1.FacetBinding{
					{Facet: "completable"},
					{
						Facet:    "schedulable",
						Bindings: map[string]string{"due": `item.mirror.data["task"].due`},
					},
				},
				Types: types,
			},
		},
		// Starter rule: the user copies it in via RuleService and edits freely.
		// This is the bidirectional half — local completion pushed back to the
		// file as a set_completed intent.
		RuleTemplates: []*taskcorev1.Rule{
			{
				Name:        "todotxt-writeback",
				Description: "Push local completion back to the todo.txt file",
				Became:      `kind == "todotxt.task" && completed`,
				Do:          &taskcorev1.RuleActions{Intent: "set_completed"},
			},
		},
	}
}

// Connector implements taskplugin.Connector, Resolver, and IntentHandler for
// one configured todo.txt file. Snapshot mirrors the file; HandleIntent
// rewrites a single line atomically and returns the re-parsed, file-confirmed
// item (DESIGN.md §9: the mirror only ever holds remote-confirmed truth).
type Connector struct {
	// Now is injectable for deterministic completion dates; nil means
	// time.Now.
	Now func() time.Time
	Log *slog.Logger

	mu  sync.Mutex
	cfg Config
}

var (
	_ taskplugin.Connector     = (*Connector)(nil)
	_ taskplugin.Resolver      = (*Connector)(nil)
	_ taskplugin.IntentHandler = (*Connector)(nil)
)

// Config is the per-instance connector configuration.
type Config struct {
	// Path is the absolute path of the todo.txt file. It must exist at
	// Configure time.
	Path string
}

// ParseConfig validates the instance config struct. The only accepted field is
// "path": an absolute path to an existing file. Unknown fields are rejected so
// typos surface at configure time.
func ParseConfig(s *structpb.Struct) (Config, error) {
	if s == nil {
		return Config{}, status.Error(codes.InvalidArgument, `todotxt config: missing config (need {"path": ...})`)
	}
	fields := s.GetFields()

	var unknown []string
	for k := range fields {
		if k != "path" {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return Config{}, status.Errorf(codes.InvalidArgument,
			"todotxt config: unknown field(s) %s (accepted: path)", strings.Join(unknown, ", "))
	}

	pf, ok := fields["path"]
	if !ok {
		return Config{}, status.Error(codes.InvalidArgument, `todotxt config: required field "path" is missing`)
	}
	sv, ok := pf.GetKind().(*structpb.Value_StringValue)
	if !ok {
		return Config{}, status.Error(codes.InvalidArgument, `todotxt config: field "path" must be a string`)
	}
	p := strings.TrimSpace(sv.StringValue)
	if p == "" {
		return Config{}, status.Error(codes.InvalidArgument, `todotxt config: field "path" must not be empty`)
	}
	if !filepath.IsAbs(p) {
		return Config{}, status.Errorf(codes.InvalidArgument, `todotxt config: field "path" must be an absolute path, got %q`, p)
	}
	info, err := os.Stat(p)
	if err != nil {
		return Config{}, status.Errorf(codes.InvalidArgument, "todotxt config: path %q must exist at configure time: %v", p, err)
	}
	if info.IsDir() {
		return Config{}, status.Errorf(codes.InvalidArgument, "todotxt config: path %q is a directory, want a file", p)
	}
	return Config{Path: p}, nil
}

func (c *Connector) Configure(ctx context.Context, instance string, config *structpb.Struct) (*pluginv1.Capabilities, error) {
	cfg, err := ParseConfig(config)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.cfg = cfg
	c.mu.Unlock()
	return &pluginv1.Capabilities{
		Enumeration:  pluginv1.Enumeration_ENUMERATION_SNAPSHOT,
		PollInterval: durationpb.New(time.Minute),
		Intents: []*pluginv1.KindIntents{{
			Kind: KindTask,
			Intents: []pluginv1.Intent{
				pluginv1.Intent_INTENT_RENAME,
				pluginv1.Intent_INTENT_SET_COMPLETED,
				pluginv1.Intent_INTENT_SET_DUE,
			},
		}},
	}, nil
}

// Snapshot mirrors every task line in file order. Duplicate external_ids
// (identical untagged text appearing twice) are skipped after the first, with a
// warning — the core's snapshot diffing requires per-snapshot uniqueness.
func (c *Connector) Snapshot(ctx context.Context, emit func(*pluginv1.RemoteItem) error) error {
	c.mu.Lock()
	path := c.cfg.Path
	c.mu.Unlock()

	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("todotxt snapshot: read %s: %w", path, err)
	}
	lines, warnings := Parse(content)
	c.logWarnings(warnings)

	seen := make(map[string]bool, len(lines))
	for _, l := range lines {
		id := l.ExternalID()
		if seen[id] {
			if c.Log != nil {
				c.Log.Warn("todotxt: duplicate external_id skipped", "external_id", id)
			}
			continue
		}
		seen[id] = true
		item, err := itemForLine(l)
		if err != nil {
			return err
		}
		if err := emit(item); err != nil {
			return err
		}
	}
	return nil
}

// Resolve fetches one task by reference for attach-to-remote and pinned
// refresh: an external_id (an id: tag value or an "h:<hash>") or the literal
// task text (the raw description).
func (c *Connector) Resolve(ctx context.Context, ref string) (*pluginv1.RemoteItem, error) {
	c.mu.Lock()
	path := c.cfg.Path
	c.mu.Unlock()

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("todotxt resolve: read %s: %w", path, err)
	}
	lines, _ := Parse(content)

	for _, l := range lines {
		if l.ExternalID() == ref {
			return itemForLine(l)
		}
	}
	trimmed := strings.TrimSpace(ref)
	for _, l := range lines {
		if strings.TrimSpace(l.Description) == trimmed {
			return itemForLine(l)
		}
	}
	return nil, status.Errorf(codes.NotFound, "todotxt resolve: no task matching %q", ref)
}

// HandleIntent applies one intent to the target line and rewrites the whole
// file atomically, changing only that line (plus the id: stabilization that
// freezes the caller's identity — see the package doc). It returns the item
// re-parsed from the line it actually wrote.
func (c *Connector) HandleIntent(ctx context.Context, req *pluginv1.HandleIntentRequest) (*pluginv1.RemoteItem, error) {
	c.mu.Lock()
	path := c.cfg.Path
	c.mu.Unlock()

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("todotxt intent: stat %s: %w", path, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("todotxt intent: read %s: %w", path, err)
	}

	// Split preserving structure: every "\n" survives, so untouched lines
	// (and the trailing newline) are rewritten byte-for-byte.
	parts := strings.Split(string(content), "\n")
	idx, target, found := locate(parts, req.GetExternalId())
	if !found {
		return nil, status.Errorf(codes.NotFound, "todotxt intent: no task with external_id %q", req.GetExternalId())
	}

	edited, err := c.applyIntent(target, req.GetExternalId(), req)
	if err != nil {
		return nil, err
	}

	newLine := edited.String()
	if strings.HasSuffix(parts[idx], "\r") { // keep CRLF line endings consistent
		newLine += "\r"
	}
	parts[idx] = newLine
	if err := writeFileAtomic(path, []byte(strings.Join(parts, "\n")), info.Mode().Perm()); err != nil {
		return nil, fmt.Errorf("todotxt intent: write %s: %w", path, err)
	}

	confirmed, _ := parseLine(newLine)
	return itemForLine(confirmed)
}

// locate finds the line addressed by externalID: an id: tag match wins over a
// hash match (a stabilized line whose id: value equals externalID beats any
// untagged line that merely hashes to the same value).
func locate(parts []string, externalID string) (int, Line, bool) {
	for i, raw := range parts {
		if skipLine(raw) {
			continue
		}
		if l, _ := parseLine(raw); l.ID != "" && l.ID == externalID {
			return i, l, true
		}
	}
	for i, raw := range parts {
		if skipLine(raw) {
			continue
		}
		if l, _ := parseLine(raw); l.ID == "" && l.ExternalID() == externalID {
			return i, l, true
		}
	}
	return 0, Line{}, false
}

// applyIntent produces the edited Line. externalID is the identity the caller
// addressed; when the line is untagged it becomes the value of a new id: tag so
// the identity survives the edit. Operations are idempotent (they set state
// rather than toggle it), so re-applying the same intent yields the same line.
func (c *Connector) applyIntent(l Line, externalID string, req *pluginv1.HandleIntentRequest) (Line, error) {
	words := strings.Fields(l.Description)
	if l.ID == "" { // stabilize: durable id: tag equal to the addressed external_id
		words = append(words, "id:"+externalID)
	}

	switch req.GetIntent() {
	case pluginv1.Intent_INTENT_RENAME:
		title, err := stringParam(req.GetParams(), "title")
		if err != nil {
			return Line{}, err
		}
		if title = strings.TrimSpace(title); title == "" {
			return Line{}, status.Error(codes.InvalidArgument, `todotxt rename: "title" must not be empty`)
		}
		var tags []string
		for _, w := range words {
			if classifyWord(w) != wordFree {
				tags = append(tags, w)
			}
		}
		words = append(strings.Fields(title), tags...)

	case pluginv1.Intent_INTENT_SET_DUE:
		val, clear, err := dueParam(req.GetParams())
		if err != nil {
			return Line{}, err
		}
		if clear {
			words = removeKVTag(words, "due")
		} else {
			words = setKVTag(words, "due", val)
		}

	case pluginv1.Intent_INTENT_SET_COMPLETED:
		done, err := boolParam(req.GetParams(), "completed")
		if err != nil {
			return Line{}, err
		}
		if done {
			if !l.Completed {
				l.Completed = true
				l.CompletionDate = c.today()
				if l.Priority != "" { // drop (A), preserve as pri: tag
					words = setPriTag(words, l.Priority)
					l.Priority = ""
				}
			}
		} else if l.Completed {
			l.Completed = false
			l.CompletionDate = ""
			if p := priFromWords(words); p != "" { // restore pri: -> (A)
				words = removeKVTag(words, "pri")
				l.Priority = p
			}
		}

	default:
		return Line{}, status.Errorf(codes.InvalidArgument, "todotxt: unsupported intent %v", req.GetIntent())
	}

	l.Description = strings.Join(words, " ")
	return l, nil
}

func (c *Connector) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Connector) today() string {
	return c.now().UTC().Format(dateLayout)
}

func (c *Connector) logWarnings(warnings []string) {
	if c.Log == nil {
		return
	}
	for _, w := range warnings {
		c.Log.Warn("todotxt parse warning", "warning", w)
	}
}

// itemForLine builds the RemoteItem and typed Task payload for one line.
func itemForLine(l Line) (*pluginv1.RemoteItem, error) {
	task := &todotxtpluginv1.Task{
		Priority:    l.EffectivePriority(),
		Projects:    l.Projects,
		Contexts:    l.Contexts,
		Due:         dateToTimestamp(l.Due),
		CreatedOn:   dateToTimestamp(l.CreationDate),
		CompletedOn: dateToTimestamp(l.CompletionDate),
	}
	a, err := anypb.New(task)
	if err != nil {
		return nil, fmt.Errorf("todotxt: encode task payload: %w", err)
	}
	return &pluginv1.RemoteItem{
		ExternalId: l.ExternalID(),
		Kind:       KindTask,
		Title:      stripTags(l.Description),
		State:      l.State(),
		Data:       map[string]*anypb.Any{"task": a},
	}, nil
}

// dateToTimestamp converts a YYYY-MM-DD string to midnight UTC, or nil.
func dateToTimestamp(d string) *timestamppb.Timestamp {
	if d == "" {
		return nil
	}
	t, err := time.Parse(dateLayout, d)
	if err != nil {
		return nil
	}
	return timestamppb.New(t.UTC())
}

// setKVTag replaces the first key:<...> word in place, or appends key:val.
func setKVTag(words []string, key, val string) []string {
	for i, w := range words {
		if k, _, ok := splitKV(w); ok && k == key {
			words[i] = key + ":" + val
			return words
		}
	}
	return append(words, key+":"+val)
}

// removeKVTag drops every key:<...> word.
func removeKVTag(words []string, key string) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		if k, _, ok := splitKV(w); ok && k == key {
			continue
		}
		out = append(out, w)
	}
	return out
}

// setPriTag appends pri:<p> unless a pri: tag already exists.
func setPriTag(words []string, p string) []string {
	for _, w := range words {
		if k, _, ok := splitKV(w); ok && k == "pri" {
			return words
		}
	}
	return append(words, "pri:"+p)
}

// priFromWords returns the pri: tag's single-letter value, or "".
func priFromWords(words []string) string {
	for _, w := range words {
		if k, v, ok := splitKV(w); ok && k == "pri" && isPriorityLetter(v) {
			return v
		}
	}
	return ""
}

func stringParam(p *structpb.Struct, key string) (string, error) {
	v, ok := p.GetFields()[key]
	if !ok {
		return "", status.Errorf(codes.InvalidArgument, "todotxt: missing %q param", key)
	}
	sv, ok := v.GetKind().(*structpb.Value_StringValue)
	if !ok {
		return "", status.Errorf(codes.InvalidArgument, "todotxt: %q must be a string", key)
	}
	return sv.StringValue, nil
}

func boolParam(p *structpb.Struct, key string) (bool, error) {
	v, ok := p.GetFields()[key]
	if !ok {
		return false, status.Errorf(codes.InvalidArgument, "todotxt: missing %q param", key)
	}
	bv, ok := v.GetKind().(*structpb.Value_BoolValue)
	if !ok {
		return false, status.Errorf(codes.InvalidArgument, "todotxt: %q must be a bool", key)
	}
	return bv.BoolValue, nil
}

// dueParam reads the SET_DUE "due" parameter: an RFC3339 string sets the due:
// tag (date part, in UTC); an absent field, a null, or an empty string clears
// it.
func dueParam(p *structpb.Struct) (val string, clear bool, err error) {
	v, ok := p.GetFields()["due"]
	if !ok {
		return "", true, nil
	}
	switch k := v.GetKind().(type) {
	case *structpb.Value_NullValue:
		return "", true, nil
	case *structpb.Value_StringValue:
		s := strings.TrimSpace(k.StringValue)
		if s == "" {
			return "", true, nil
		}
		t, perr := time.Parse(time.RFC3339, s)
		if perr != nil {
			return "", false, status.Errorf(codes.InvalidArgument, "todotxt set_due: %q must be an RFC3339 timestamp: %v", s, perr)
		}
		return t.UTC().Format(dateLayout), false, nil
	default:
		return "", false, status.Error(codes.InvalidArgument, "todotxt set_due: due must be an RFC3339 string or null")
	}
}

// writeFileAtomic writes data to a temp file in the same directory and renames
// it over path, so a crash mid-write never leaves a truncated todo.txt.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	remove = false
	return nil
}
