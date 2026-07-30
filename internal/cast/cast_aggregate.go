//go:build cast

package cast

import (
	"context"
	"fmt"
	"sync"
)

// aggregateCaster implements Caster over one or more protocol backends. It
// merges their discovery streams (deduped) and routes a cast to the backend
// that owns the target device.
type aggregateCaster struct {
	backends []backend
}

func newAggregateCaster(backends ...backend) *aggregateCaster {
	return &aggregateCaster{backends: backends}
}

// Discover fans in every backend's device stream and emits the merged list on
// any change, until ctx is cancelled and all backends stop.
func (a *aggregateCaster) Discover(ctx context.Context) (<-chan []Device, error) {
	out := make(chan []Device)
	if len(a.backends) == 0 {
		close(out)
		return out, nil
	}

	var mu sync.Mutex
	latest := make([][]Device, len(a.backends))

	emit := func() {
		mu.Lock()
		merged := mergeDevices(latest)
		mu.Unlock()
		select {
		case out <- merged:
		case <-ctx.Done():
		}
	}

	var wg sync.WaitGroup
	for i, b := range a.backends {
		ch, err := b.Discover(ctx)
		if err != nil || ch == nil {
			continue
		}
		wg.Add(1)
		go func(i int, ch <-chan []Device) {
			defer wg.Done()
			for devs := range ch {
				mu.Lock()
				latest[i] = devs
				mu.Unlock()
				emit()
			}
		}(i, ch)
	}

	go func() {
		wg.Wait()
		close(out)
	}()
	return out, nil
}

// mergeDevices flattens per-backend device lists into one, deduped by kind+id.
func mergeDevices(lists [][]Device) []Device {
	seen := make(map[string]bool)
	var merged []Device
	for _, list := range lists {
		for _, d := range list {
			key := string(d.Kind) + "|" + d.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, d)
		}
	}
	return merged
}

func (a *aggregateCaster) Cast(ctx context.Context, dev Device, item MediaItem) (Session, error) {
	return a.CastQueue(ctx, dev, []MediaItem{item}, 0)
}

func (a *aggregateCaster) CastQueue(ctx context.Context, dev Device, items []MediaItem, startIndex int) (Session, error) {
	b := a.backendFor(dev)
	if b == nil {
		return nil, fmt.Errorf("cast: no backend for device %q (%s)", dev.Name, dev.Kind)
	}
	r, err := b.Connect(ctx, dev)
	if err != nil {
		return nil, fmt.Errorf("cast: connect to %q: %w", dev.Name, err)
	}
	sess, err := newQueueSession(r, items, startIndex)
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	return sess, nil
}

func (a *aggregateCaster) backendFor(dev Device) backend {
	for _, b := range a.backends {
		if b.Handles(dev.Kind) {
			return b
		}
	}
	return nil
}

// Close is a no-op: backends stop when the discovery ctx is cancelled, and
// sessions own their own renderer lifecycle.
func (a *aggregateCaster) Close() error { return nil }
