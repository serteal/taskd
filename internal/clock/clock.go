// Package clock provides injectable time and ID generation. Determinism for
// tests is a design-level requirement: every component that reads the clock
// or mints IDs takes these interfaces, never time.Now or rand directly.
package clock

import (
	"math/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Clock is the single source of time.
type Clock interface {
	Now() time.Time
}

// IDGen mints item IDs (ULIDs: lexically sortable, timestamp-prefixed).
type IDGen interface {
	NewID() string
}

// System returns the real clock.
func System() Clock { return systemClock{} }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// NewIDGen returns a ULID generator driven by the given clock and a
// deterministic-capable entropy source. Pass seed 0 for crypto-quality
// entropy in production; a fixed seed yields a reproducible ID sequence.
func NewIDGen(c Clock, seed int64) IDGen {
	var entropy *ulid.MonotonicEntropy
	if seed != 0 {
		entropy = ulid.Monotonic(rand.New(rand.NewSource(seed)), 0)
	} else {
		entropy = ulid.Monotonic(ulid.DefaultEntropy(), 0)
	}
	return &ulidGen{clock: c, entropy: entropy}
}

type ulidGen struct {
	mu      sync.Mutex
	clock   Clock
	entropy *ulid.MonotonicEntropy
}

func (g *ulidGen) NewID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return ulid.MustNew(ulid.Timestamp(g.clock.Now()), g.entropy).String()
}

// Fake is a manually advanced clock for tests.
type Fake struct {
	mu sync.Mutex
	t  time.Time
}

// NewFake starts a fake clock at t.
func NewFake(t time.Time) *Fake { return &Fake{t: t.UTC()} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Advance moves the clock forward by d and returns the new time.
func (f *Fake) Advance(d time.Duration) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
	return f.t
}
