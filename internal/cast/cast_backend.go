//go:build cast

package cast

import "context"

// playbackState is a device's normalized transport state, used by the queue
// manager to decide when to advance to the next item.
type playbackState int

const (
	stateUnknown playbackState = iota
	statePlaying
	statePaused
	stateIdle
	stateEnded // finished the current item; queue should advance
)

func (p playbackState) String() string {
	switch p {
	case statePlaying:
		return "playing"
	case statePaused:
		return "paused"
	default:
		return "idle"
	}
}

// renderer controls playback on one connected device. Implementations wrap a
// single protocol (Chromecast, DLNA); the queue manager drives them and is the
// only caller. All methods are best-effort and return the device's error.
type renderer interface {
	// Load starts playing a single item, replacing whatever is playing.
	Load(ctx context.Context, item MediaItem) error
	Play() error
	Pause() error
	Stop() error
	// State reports the current transport state for queue advancement/status.
	State() (playbackState, error)
	// Close releases the connection to the device.
	Close() error
}

// backend is one cast protocol. It discovers devices and opens a renderer to a
// chosen device. A single aggregateCaster fronts several backends.
type backend interface {
	// Discover emits the full current device list whenever it changes, until
	// ctx is cancelled, then closes the channel.
	Discover(ctx context.Context) (<-chan []Device, error)
	// Connect opens a control session to a device this backend discovered.
	Connect(ctx context.Context, dev Device) (renderer, error)
	// Handles reports whether this backend owns a device of the given kind.
	Handles(kind DeviceKind) bool
}
