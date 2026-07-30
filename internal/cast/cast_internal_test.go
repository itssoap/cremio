//go:build cast

package cast

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeRenderer is a controllable in-memory renderer for testing the queue
// manager without a real device.
type fakeRenderer struct {
	mu     sync.Mutex
	loaded []MediaItem
	state  playbackState
	closed bool
}

func (f *fakeRenderer) Load(_ context.Context, item MediaItem) error {
	f.mu.Lock()
	f.loaded = append(f.loaded, item)
	f.state = statePlaying
	f.mu.Unlock()
	return nil
}
func (f *fakeRenderer) Play() error  { f.set(statePlaying); return nil }
func (f *fakeRenderer) Pause() error { f.set(statePaused); return nil }
func (f *fakeRenderer) Stop() error  { f.set(stateIdle); return nil }
func (f *fakeRenderer) State() (playbackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}
func (f *fakeRenderer) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}
func (f *fakeRenderer) set(s playbackState) { f.mu.Lock(); f.state = s; f.mu.Unlock() }
func (f *fakeRenderer) lastURL() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.loaded) == 0 {
		return ""
	}
	return f.loaded[len(f.loaded)-1].URL
}

func threeItems() []MediaItem {
	return []MediaItem{{URL: "a", Title: "A"}, {URL: "b", Title: "B"}, {URL: "c", Title: "C"}}
}

// disableAutoTick stops the watcher ticker from firing so tests drive advance()
// deterministically.
func disableAutoTick(t *testing.T) {
	old := queuePollInterval
	queuePollInterval = time.Hour
	t.Cleanup(func() { queuePollInterval = old })
}

func TestQueueSessionAdvancesOnEnd(t *testing.T) {
	disableAutoTick(t)
	r := &fakeRenderer{}
	s, err := newQueueSession(r, threeItems(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	if r.lastURL() != "a" {
		t.Fatalf("first load = %q, want a", r.lastURL())
	}
	r.set(stateEnded)
	s.advance()
	if r.lastURL() != "b" {
		t.Fatalf("after A ended, load = %q, want b", r.lastURL())
	}
	r.set(stateEnded)
	s.advance()
	if r.lastURL() != "c" {
		t.Fatalf("after B ended, load = %q, want c", r.lastURL())
	}
	// C ends -> past the end -> the session stops.
	r.set(stateEnded)
	s.advance()
	st, _ := s.Status()
	if st.State != "idle" {
		t.Fatalf("after last item, state = %q, want idle", st.State)
	}
}

func TestQueueSessionNextPrev(t *testing.T) {
	disableAutoTick(t)
	r := &fakeRenderer{}
	s, err := newQueueSession(r, threeItems(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	if err := s.Next(); err != nil {
		t.Fatal(err)
	}
	if r.lastURL() != "b" {
		t.Fatalf("Next -> %q, want b", r.lastURL())
	}
	if err := s.Prev(); err != nil {
		t.Fatal(err)
	}
	if r.lastURL() != "a" {
		t.Fatalf("Prev -> %q, want a", r.lastURL())
	}
	// Prev at the head stays on the first item.
	_ = s.Prev()
	if r.lastURL() != "a" {
		t.Fatalf("Prev at head -> %q, want a", r.lastURL())
	}
}

func TestNewQueueSessionRejectsEmpty(t *testing.T) {
	if _, err := newQueueSession(&fakeRenderer{}, nil, 0); err == nil {
		t.Fatal("empty queue should error")
	}
}

// fakeBackend emits a fixed device list once, then closes discovery.
type fakeBackend struct {
	kind    DeviceKind
	devices []Device
}

func (b *fakeBackend) Discover(context.Context) (<-chan []Device, error) {
	ch := make(chan []Device, 1)
	ch <- b.devices
	close(ch)
	return ch, nil
}
func (b *fakeBackend) Connect(context.Context, Device) (renderer, error) { return &fakeRenderer{}, nil }
func (b *fakeBackend) Handles(k DeviceKind) bool                         { return k == b.kind }

func TestAggregateDiscoverMergesBackends(t *testing.T) {
	b1 := &fakeBackend{kind: KindChromecast, devices: []Device{{ID: "1", Name: "CC", Kind: KindChromecast}}}
	b2 := &fakeBackend{kind: KindDLNA, devices: []Device{{ID: "2", Name: "TV", Kind: KindDLNA}}}
	a := newAggregateCaster(b1, b2)

	ch, err := a.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var last []Device
	for devs := range ch { // drain until discovery closes
		last = devs
	}
	if len(last) != 2 {
		t.Fatalf("merged device count = %d, want 2", len(last))
	}
}

func TestAggregateRoutesToOwningBackend(t *testing.T) {
	a := newAggregateCaster(&fakeBackend{kind: KindDLNA})
	// A chromecast device has no backend here.
	if _, err := a.Cast(context.Background(), Device{Kind: KindChromecast}, MediaItem{URL: "x"}); err == nil {
		t.Fatal("casting to a device with no backend should error")
	}
	// A DLNA device is handled.
	sess, err := a.Cast(context.Background(), Device{Kind: KindDLNA}, MediaItem{URL: "https://x/y.mkv"})
	if err != nil {
		t.Fatalf("expected cast to succeed: %v", err)
	}
	_ = sess.Stop()
}
