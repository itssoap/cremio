//go:build cast

package cast

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// queuePollInterval is how often the queue manager polls the device to detect
// end-of-item so it can advance. A package var so tests can shrink it.
var queuePollInterval = 1500 * time.Millisecond

// queueSession implements Session with a protocol-agnostic, client-side queue:
// it loads one item at a time on the renderer and advances when the device
// reports the item ended. This makes playlist casting work identically over
// Chromecast and DLNA, both of which play a single URI at a time.
type queueSession struct {
	r     renderer
	items []MediaItem

	interval time.Duration // snapshot of queuePollInterval, so watch() never
	// races with a test mutating the package var

	mu      sync.Mutex
	idx     int
	stopped bool
	quit    chan struct{}
}

func newQueueSession(r renderer, items []MediaItem, startIndex int) (*queueSession, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("cast: empty queue")
	}
	if startIndex < 0 || startIndex >= len(items) {
		startIndex = 0
	}
	s := &queueSession{r: r, items: items, idx: startIndex, interval: queuePollInterval, quit: make(chan struct{})}
	if err := s.loadCurrent(); err != nil {
		return nil, err
	}
	go s.watch()
	return s, nil
}

func (s *queueSession) loadCurrent() error {
	s.mu.Lock()
	item := s.items[s.idx]
	s.mu.Unlock()
	return s.r.Load(context.Background(), item)
}

// watch polls for end-of-item and advances the queue until stopped.
func (s *queueSession) watch() {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-t.C:
			s.advance()
		}
	}
}

// advance loads the next item when the current one has ended. Split out from
// watch so tests can drive it deterministically without waiting on the ticker.
func (s *queueSession) advance() {
	s.mu.Lock()
	stopped := s.stopped
	s.mu.Unlock()
	if stopped {
		return
	}
	st, err := s.r.State()
	if err != nil {
		return
	}
	if st == stateEnded {
		_ = s.Next()
	}
}

func (s *queueSession) Next() error {
	s.mu.Lock()
	if s.idx+1 >= len(s.items) {
		s.mu.Unlock()
		return s.Stop() // past the end of the queue
	}
	s.idx++
	s.mu.Unlock()
	return s.loadCurrent()
}

func (s *queueSession) Prev() error {
	s.mu.Lock()
	if s.idx > 0 {
		s.idx--
	}
	s.mu.Unlock()
	return s.loadCurrent()
}

func (s *queueSession) Play() error  { return s.r.Play() }
func (s *queueSession) Pause() error { return s.r.Pause() }

func (s *queueSession) Stop() error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	close(s.quit)
	s.mu.Unlock()
	err := s.r.Stop()
	_ = s.r.Close()
	return err
}

func (s *queueSession) Status() (SessionStatus, error) {
	s.mu.Lock()
	idx := s.idx
	title := s.items[idx].Title
	stopped := s.stopped
	s.mu.Unlock()

	if stopped {
		return SessionStatus{State: "idle", Index: idx, Title: title}, nil
	}
	st, err := s.r.State()
	if err != nil {
		return SessionStatus{}, err
	}
	return SessionStatus{State: st.String(), Index: idx, Title: title}, nil
}
