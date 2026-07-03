package feed

import (
	"sync"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// Hub fans events out to live watchers. Delivery is best-effort per
// subscriber: a subscriber that falls behind its buffer is marked lagged and
// closed, and recovers via the store-backed catch-up path (the client SDK
// resumes from its last cursor; the log, not the hub, is the source of
// truth). Publish never blocks on slow consumers.
type Hub struct {
	mu   sync.Mutex
	subs map[int]*Subscription
	next int
}

func NewHub() *Hub {
	return &Hub{subs: make(map[int]*Subscription)}
}

// Publish is called after an event has committed to the store.
func (h *Hub) Publish(e *taskcorev1.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, s := range h.subs {
		select {
		case s.ch <- e:
		default:
			s.lagged = true
			close(s.ch)
			delete(h.subs, id)
		}
	}
}

// Subscribe registers a live-event subscription with the given buffer.
// Subscribe before reading history from the store, then dedup by cursor:
// events published while catching up wait in the buffer.
func (h *Hub) Subscribe(buffer int) *Subscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := &Subscription{ch: make(chan *taskcorev1.Event, buffer), hub: h, id: h.next}
	h.subs[h.next] = s
	h.next++
	return s
}

// Subscription is one watcher's live feed.
type Subscription struct {
	ch     chan *taskcorev1.Event
	hub    *Hub
	id     int
	lagged bool
	once   sync.Once
}

// C yields events until the subscription is closed. A close without Close()
// being called means the subscriber lagged (see Lagged).
func (s *Subscription) C() <-chan *taskcorev1.Event { return s.ch }

// Lagged reports whether the hub dropped this subscriber for falling behind.
// Valid after C() is closed.
func (s *Subscription) Lagged() bool {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	return s.lagged
}

// Close detaches the subscription. Safe to call multiple times and
// concurrently with Publish.
func (s *Subscription) Close() {
	s.once.Do(func() {
		s.hub.mu.Lock()
		defer s.hub.mu.Unlock()
		if _, ok := s.hub.subs[s.id]; ok {
			delete(s.hub.subs, s.id)
			close(s.ch)
		}
	})
}
