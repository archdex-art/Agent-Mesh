package hub

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/agentmesh/agentmesh/services/realtime-gateway/internal/pubsub"
)

// fakeSubscriber records which projects were subscribed/canceled so tests
// can assert the Hub only pays for a Redis subscription while at least
// one local client is listening (the lazy start/stop behavior hub.go
// documents).
type fakeSubscriber struct {
	mu          sync.Mutex
	subscribed  []string
	blockUntils map[string]context.Context
}

func newFakeSubscriber() *fakeSubscriber {
	return &fakeSubscriber{blockUntils: make(map[string]context.Context)}
}

func (f *fakeSubscriber) SubscribeProject(ctx context.Context, projectID string) error {
	f.mu.Lock()
	f.subscribed = append(f.subscribed, projectID)
	f.blockUntils[projectID] = ctx
	f.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeSubscriber) wasSubscribed(projectID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.subscribed {
		if p == projectID {
			return true
		}
	}
	return false
}

func (f *fakeSubscriber) isCanceled(projectID string) bool {
	f.mu.Lock()
	ctx, ok := f.blockUntils[projectID]
	f.mu.Unlock()
	if !ok {
		return false
	}
	return ctx.Err() != nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSubscribeStartsRedisSubscriptionOnFirstClient(t *testing.T) {
	fake := newFakeSubscriber()
	h := New(discardLogger())
	h.AttachSubscriber(fake)

	_, unsubscribe := h.Subscribe("proj-1")
	defer unsubscribe()

	deadline := time.After(time.Second)
	for !fake.wasSubscribed("proj-1") {
		select {
		case <-deadline:
			t.Fatal("expected SubscribeProject to be called for proj-1")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestUnsubscribeStopsRedisSubscriptionWhenLastClientLeaves(t *testing.T) {
	fake := newFakeSubscriber()
	h := New(discardLogger())
	h.AttachSubscriber(fake)

	_, unsub1 := h.Subscribe("proj-2")
	_, unsub2 := h.Subscribe("proj-2")

	unsub1()
	if fake.isCanceled("proj-2") {
		t.Fatal("subscription should stay alive while one client remains")
	}

	unsub2()
	deadline := time.After(time.Second)
	for !fake.isCanceled("proj-2") {
		select {
		case <-deadline:
			t.Fatal("expected the redis subscription to be canceled once the last client unsubscribed")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestBroadcastDeliversToAllSubscribedClients(t *testing.T) {
	fake := newFakeSubscriber()
	h := New(discardLogger())
	h.AttachSubscriber(fake)

	events1, unsub1 := h.Subscribe("proj-3")
	defer unsub1()
	events2, unsub2 := h.Subscribe("proj-3")
	defer unsub2()

	want := pubsub.SpanEvent{TraceID: "t1", SpanID: "s1", Kind: "llm.call", Name: "gpt-4o", Status: "ok"}
	h.Broadcast("proj-3", want)

	for _, ch := range []<-chan pubsub.SpanEvent{events1, events2} {
		select {
		case got := <-ch:
			if got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for broadcast event")
		}
	}
}

func TestBroadcastToUnknownProjectIsNoop(t *testing.T) {
	fake := newFakeSubscriber()
	h := New(discardLogger())
	h.AttachSubscriber(fake)

	// Must not panic or block when no one has subscribed to this project.
	h.Broadcast("nobody-listening", pubsub.SpanEvent{TraceID: "t1"})
}
