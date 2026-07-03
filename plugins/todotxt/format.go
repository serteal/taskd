// Package todotxt is a bidirectional connector for a single todo.txt file
// (http://todotxt.org). It mirrors every task line as a RemoteItem and handles
// write intents by rewriting the target line in place, leaving every other
// byte of the file untouched (DESIGN.md §9: intents return remote-confirmed
// state). The todo.txt grammar is implemented by hand here, stdlib only.
//
// # Line grammar
//
//		[x ] [(A) ] [completion-date ] [creation-date ] description +proj @ctx key:value
//
//	  - A leading "x " marks the task complete. On a completed line an optional
//	    completion date (YYYY-MM-DD) comes first, then an optional creation date.
//	  - A priority "(A)".."(Z)" appears only on incomplete tasks. On completion
//	    the priority is dropped from that slot and preserved as a pri:<letter>
//	    tag inside the description (a common convention); set_completed(false)
//	    restores it to (A) form.
//	  - An incomplete task may carry only a creation date (no completion date).
//	  - +project and @context tags and key:value tags live anywhere in the
//	    description. due:YYYY-MM-DD and id:<token> are recognized; every other
//	    key:value tag is preserved verbatim.
//	  - Blank lines and lines whose first non-space rune is '#' are legal padding
//	    and are skipped without warning.
//	  - A token in a date slot that looks like a date (NNNN-NN-NN) but is not a
//	    real calendar date produces a warning and is treated as plain text.
//
// Serialization (String) is canonical and deterministic; for well-formed input
// Parse -> String -> Parse yields identical Lines.
//
// # Identity (frozen contract — must never change once shipped)
//
// A task line's external_id is:
//
//   - the value of its id: tag, when the line carries one; otherwise
//   - "h:" followed by the first 12 lowercase hex characters of the SHA-256 of
//     the line's trimmed description text (inline tags included, the structural
//     x / priority / date prefixes excluded).
//
// external_id grammar:
//
//	tagged line:    <id-tag value>          e.g. id:pr-42       -> "pr-42"
//	untagged line:  "h:" <12 lowercase hex> e.g. "buy milk"     -> "h:9f86d081884c"
//
// HandleIntent stabilizes identity on the first write to an untagged line: the
// line is addressed by its "h:<hash>" id, and the edit adds a tag whose value
// IS that same "h:<hash>", so the tag literally reads id:h:<hash>. The
// description the hash was taken from is about to change, so freezing it as a
// tag keeps the identity the caller already holds durable. Tagged lines keep
// their id: tag forever; renames and every other edit leave id: (and all
// +project/@context/key:value) tags untouched.
package todotxt

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	// KindTask is the RemoteItem kind for every todo.txt task line.
	KindTask = "todotxt.task"
	// StateOpen / StateDone are the two mirror states a task line can carry.
	StateOpen = "open"
	StateDone = "done"

	dateLayout = "2006-01-02"
)

// Tag is one key:value tag parsed from a description, in appearance order.
type Tag struct {
	Key   string
	Value string
}

// Line is one parsed todo.txt task line. Description keeps the free text WITH
// its inline tags but WITHOUT the structural x/priority/date prefixes; the
// remaining fields are parsed views over it. Dates are kept as YYYY-MM-DD
// strings so serialization round-trips exactly.
type Line struct {
	Completed      bool
	Priority       string // "A".."Z" (incomplete tasks only), else ""
	CompletionDate string // YYYY-MM-DD, completed tasks only
	CreationDate   string // YYYY-MM-DD
	Description    string // free text + inline tags, prefixes stripped

	// Parsed views over Description.
	Projects []string // +project tags without the '+'
	Contexts []string // @context tags without the '@'
	Tags     []Tag    // every key:value tag in appearance order (due, id, pri, ...)
	Due      string   // due: value when it is a valid date, else ""
	ID       string   // id: value when present, else ""
}

// Parse reads a whole todo.txt file. Blank lines and '#' comments are skipped;
// every other line is parsed into a Line in file order. Warnings (malformed
// dates) are prefixed with their 1-based line number.
func Parse(content []byte) ([]Line, []string) {
	var (
		lines    []Line
		warnings []string
	)
	for i, raw := range strings.Split(string(content), "\n") {
		if skipLine(raw) {
			continue
		}
		l, ws := parseLine(raw)
		for _, w := range ws {
			warnings = append(warnings, fmt.Sprintf("line %d: %s", i+1, w))
		}
		lines = append(lines, l)
	}
	return lines, warnings
}

// skipLine reports whether a raw line is blank or a comment (legal padding).
func skipLine(raw string) bool {
	s := strings.TrimSpace(raw)
	return s == "" || strings.HasPrefix(s, "#")
}

// parseLine parses one non-blank, non-comment task line. A trailing "\r" (CRLF
// files) is tolerated by the leading TrimSpace.
func parseLine(raw string) (Line, []string) {
	var (
		l        Line
		warnings []string
	)
	s := strings.TrimSpace(raw)

	// Completion prefix.
	if strings.HasPrefix(s, "x ") {
		l.Completed = true
		s = strings.TrimLeft(s[2:], " ")
	}

	// Priority — incomplete tasks only.
	if !l.Completed && len(s) >= 3 && s[0] == '(' && s[2] == ')' &&
		s[1] >= 'A' && s[1] <= 'Z' && (len(s) == 3 || s[3] == ' ') {
		l.Priority = s[1:2]
		s = strings.TrimLeft(s[3:], " ")
	}

	// Dates: up to two (completion then creation) on completed tasks; one
	// (creation) on incomplete tasks.
	maxDates := 1
	if l.Completed {
		maxDates = 2
	}
	dates, rest, ws := takeDates(s, maxDates)
	warnings = append(warnings, ws...)
	if l.Completed {
		if len(dates) >= 1 {
			l.CompletionDate = dates[0]
		}
		if len(dates) >= 2 {
			l.CreationDate = dates[1]
		}
	} else if len(dates) >= 1 {
		l.CreationDate = dates[0]
	}

	l.Description = strings.TrimSpace(rest)
	l.extractTags()
	return l, warnings
}

// takeDates consumes up to max leading date tokens. A token that looks like a
// date but is not a real calendar date stops consumption and yields a warning
// (the token stays in the description as plain text).
func takeDates(s string, max int) (dates []string, rest string, warnings []string) {
	rest = s
	for len(dates) < max {
		tok, after := firstToken(rest)
		if tok == "" || !looksLikeDate(tok) {
			break
		}
		if !isValidDate(tok) {
			warnings = append(warnings, fmt.Sprintf("malformed date %q treated as text", tok))
			break
		}
		dates = append(dates, tok)
		rest = after
	}
	return dates, rest, warnings
}

// firstToken returns the first space-delimited token of s and the remainder
// with its leading spaces trimmed.
func firstToken(s string) (tok, rest string) {
	s = strings.TrimLeft(s, " ")
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], strings.TrimLeft(s[i+1:], " ")
	}
	return s, ""
}

// extractTags fills the parsed views (Projects, Contexts, Tags, Due, ID) from
// Description. First id:/due: tag wins so parsing is deterministic.
func (l *Line) extractTags() {
	for _, w := range strings.Fields(l.Description) {
		switch classifyWord(w) {
		case wordProject:
			l.Projects = append(l.Projects, w[1:])
		case wordContext:
			l.Contexts = append(l.Contexts, w[1:])
		case wordKV:
			k, v, _ := splitKV(w)
			l.Tags = append(l.Tags, Tag{Key: k, Value: v})
			switch k {
			case "due":
				if l.Due == "" && looksLikeDate(v) && isValidDate(v) {
					l.Due = v
				}
			case "id":
				if l.ID == "" {
					l.ID = v
				}
			}
		}
	}
}

// String serializes a Line in canonical field order. It is deterministic and,
// for well-formed input, round-trips: Parse(String(l)) == l.
func (l Line) String() string {
	var b strings.Builder
	if l.Completed {
		b.WriteString("x ")
		if l.CompletionDate != "" {
			b.WriteString(l.CompletionDate)
			b.WriteByte(' ')
		}
		if l.CreationDate != "" {
			b.WriteString(l.CreationDate)
			b.WriteByte(' ')
		}
	} else {
		if l.Priority != "" {
			b.WriteByte('(')
			b.WriteString(l.Priority)
			b.WriteString(") ")
		}
		if l.CreationDate != "" {
			b.WriteString(l.CreationDate)
			b.WriteByte(' ')
		}
	}
	b.WriteString(l.Description)
	return b.String()
}

// ExternalID is the frozen identity of a line (see package doc): the id: tag
// value when present, else "h:" + first 12 hex chars of sha256(description).
func (l Line) ExternalID() string {
	if l.ID != "" {
		return l.ID
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(l.Description)))
	return "h:" + hex.EncodeToString(sum[:])[:12]
}

// EffectivePriority is the task's priority: the (A) letter for incomplete
// tasks, or the pri:<letter> tag value for completed tasks. Empty when none.
func (l Line) EffectivePriority() string {
	if !l.Completed {
		return l.Priority
	}
	for _, t := range l.Tags {
		if t.Key == "pri" && isPriorityLetter(t.Value) {
			return t.Value
		}
	}
	return ""
}

// State reports the mirror state ("done" or "open").
func (l Line) State() string {
	if l.Completed {
		return StateDone
	}
	return StateOpen
}

// stripTags returns desc with every +project/@context/key:value tag removed and
// the remaining free text collapsed to single spaces — the display title.
func stripTags(desc string) string {
	var out []string
	for _, w := range strings.Fields(desc) {
		if classifyWord(w) == wordFree {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}

// Word classes used by tag handling.
const (
	wordFree = iota
	wordProject
	wordContext
	wordKV
)

func classifyWord(w string) int {
	switch {
	case len(w) > 1 && w[0] == '+':
		return wordProject
	case len(w) > 1 && w[0] == '@':
		return wordContext
	default:
		if _, _, ok := splitKV(w); ok {
			return wordKV
		}
		return wordFree
	}
}

// splitKV splits a key:value tag on its first colon. Both sides must be
// non-empty (so "http://x" is the tag key "http", value "//x" — preserved
// verbatim, harmless to identity and round-trip).
func splitKV(w string) (key, val string, ok bool) {
	i := strings.IndexByte(w, ':')
	if i <= 0 || i >= len(w)-1 {
		return "", "", false
	}
	return w[:i], w[i+1:], true
}

func looksLikeDate(tok string) bool {
	if len(tok) != 10 || tok[4] != '-' || tok[7] != '-' {
		return false
	}
	for i := 0; i < len(tok); i++ {
		if i == 4 || i == 7 {
			continue
		}
		if tok[i] < '0' || tok[i] > '9' {
			return false
		}
	}
	return true
}

func isValidDate(tok string) bool {
	_, err := time.Parse(dateLayout, tok)
	return err == nil
}

func isPriorityLetter(s string) bool {
	return len(s) == 1 && s[0] >= 'A' && s[0] <= 'Z'
}
