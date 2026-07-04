package server

import (
	"testing"

	taskpb "github.com/serteal/taskd/gen/task"
)

func TestHubFanout(t *testing.T) {
	h := NewHub()
	a, cancelA := h.Subscribe()
	b, cancelB := h.Subscribe()
	defer cancelA()
	defer cancelB()

	h.PublishDeleted("x")
	for name, ch := range map[string]<-chan *taskpb.WatchTasksResponse{"a": a, "b": b} {
		ev := <-ch
		if ev.GetDeletedTaskId() != "x" {
			t.Errorf("subscriber %s got %v", name, ev)
		}
	}

	cancelA()
	h.PublishDeleted("y")
	if ev := <-b; ev.GetDeletedTaskId() != "y" {
		t.Errorf("b after cancelA got %v", ev)
	}
	select {
	case ev, ok := <-a:
		if ok {
			t.Errorf("canceled subscriber received %v", ev)
		}
	default: // nothing delivered — also fine
	}
}

func TestHubDropsSlowSubscriber(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe()
	defer cancel()

	// Fill the buffer without reading, then one more: the hub must drop the
	// subscriber (close the channel) rather than block or buffer forever.
	for i := 0; i <= hubBuffer; i++ {
		h.PublishDeleted("t")
	}
	n := 0
	for range ch { // drains buffered events, then sees close
		n++
	}
	if n != hubBuffer {
		t.Fatalf("received %d buffered events, want %d", n, hubBuffer)
	}

	// Publishing after the drop must not panic (sub already removed).
	h.PublishDeleted("t")
	cancel() // idempotent after drop
}
