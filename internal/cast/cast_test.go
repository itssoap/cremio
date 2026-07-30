package cast

import (
	"context"
	"errors"
	"testing"
)

// The shared no-op Caster (used by the default build, and by the cast build
// until the real backends land) must never discover a device and must refuse
// to cast. This test runs under both build variants.
func TestNoopCasterBehaviour(t *testing.T) {
	c := New()
	t.Cleanup(func() { _ = c.Close() })

	ch, err := c.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover error: %v", err)
	}
	// Channel is closed with no devices ever sent.
	if devs, ok := <-ch; ok {
		t.Fatalf("expected closed empty discovery channel, got %v (open=%v)", devs, ok)
	}

	if _, err := c.Cast(context.Background(), Device{}, MediaItem{URL: "https://x/y.mkv"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Cast err = %v, want ErrUnavailable", err)
	}
	if _, err := c.CastQueue(context.Background(), Device{}, nil, 0); !errors.Is(err, ErrUnavailable) {
		t.Errorf("CastQueue err = %v, want ErrUnavailable", err)
	}
}
