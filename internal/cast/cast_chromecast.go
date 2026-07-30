//go:build cast

package cast

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vishen/go-chromecast/application"
	castproto "github.com/vishen/go-chromecast/cast"
	"github.com/vishen/go-chromecast/dns"
)

var chromecastRescanInterval = 5 * time.Second

// castApp is the subset of *application.Application the renderer uses, so the
// state logic can be unit-tested with a fake. *application.Application satisfies it.
type castApp interface {
	Load(filenameOrURL string, startTime int, contentType string, transcode, detach, forceDetach bool) error
	Pause() error
	TogglePause() error
	StopMedia() error
	Update() error
	Status() (*castproto.Application, *castproto.Media, *castproto.Volume)
	Close(stopMedia bool) error
}

// chromecastBackend discovers Chromecast / Cast-enabled devices (mDNS) and
// controls them via the Cast media protocol. The default receiver plays the
// file's default audio and cannot switch embedded tracks, so no track control
// is offered (see the plan; external subtitles are a later, opt-in feature).
type chromecastBackend struct {
	mu      sync.Mutex
	entries map[string]dns.CastEntry // device id -> mDNS entry
}

func newChromecastBackend() *chromecastBackend {
	return &chromecastBackend{entries: make(map[string]dns.CastEntry)}
}

func (b *chromecastBackend) Handles(kind DeviceKind) bool { return kind == KindChromecast }

func (b *chromecastBackend) Discover(ctx context.Context) (<-chan []Device, error) {
	out := make(chan []Device)
	go func() {
		defer close(out)
		t := time.NewTicker(chromecastRescanInterval)
		defer t.Stop()
		b.scan(ctx, out)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				b.scan(ctx, out)
			}
		}
	}()
	return out, nil
}

func (b *chromecastBackend) scan(ctx context.Context, out chan<- []Device) {
	sctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ch, err := dns.DiscoverCastDNSEntries(sctx, nil)
	if err != nil {
		return
	}
	var devs []Device
	fresh := make(map[string]dns.CastEntry)
	for e := range ch {
		id := e.UUID
		if id == "" {
			id = e.GetAddr()
		}
		name := e.DeviceName
		if name == "" {
			name = e.Name
		}
		if name == "" {
			name = e.GetAddr()
		}
		fresh[id] = e
		devs = append(devs, Device{ID: id, Name: name, Addr: e.GetAddr(), Kind: KindChromecast})
	}
	b.mu.Lock()
	b.entries = fresh
	b.mu.Unlock()
	select {
	case out <- devs:
	case <-ctx.Done():
	}
}

func (b *chromecastBackend) Connect(_ context.Context, dev Device) (renderer, error) {
	b.mu.Lock()
	e, ok := b.entries[dev.ID]
	b.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("chromecast: device %q not found; rescan and retry", dev.Name)
	}
	app := application.NewApplication(application.WithConnectionRetries(3))
	if err := app.Start(e.GetAddr(), e.GetPort()); err != nil {
		return nil, fmt.Errorf("chromecast: connect to %q: %w", dev.Name, err)
	}
	return &chromecastRenderer{app: app}, nil
}

// chromecastRenderer controls one Cast device. All app calls are serialized
// because the Cast connection is stateful and shared between the queue watcher
// (State) and user controls (Pause/Stop/...).
type chromecastRenderer struct {
	mu      sync.Mutex
	app     castApp
	started bool
}

func (r *chromecastRenderer) Load(_ context.Context, item MediaItem) error {
	mime := item.MimeType
	if mime == "" {
		mime = guessMimeFromURL(item.URL)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = false
	return r.app.Load(item.URL, 0, mime, false, false, false)
}

func (r *chromecastRenderer) Play() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, media, _ := r.app.Status()
	if media != nil && strings.EqualFold(media.PlayerState, "PAUSED") {
		return r.app.TogglePause()
	}
	return nil // already playing / unknown
}

func (r *chromecastRenderer) Pause() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.app.Pause()
}

func (r *chromecastRenderer) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.app.StopMedia()
}

func (r *chromecastRenderer) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.app.Close(false)
}

func (r *chromecastRenderer) State() (playbackState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.app.Update(); err != nil {
		return stateUnknown, err
	}
	_, media, _ := r.app.Status()
	if media == nil {
		if r.started {
			return stateEnded, nil
		}
		return statePlaying, nil // not loaded/playing yet: do not advance
	}
	switch strings.ToUpper(media.PlayerState) {
	case "PLAYING", "BUFFERING":
		r.started = true
		return statePlaying, nil
	case "PAUSED":
		r.started = true
		return statePaused, nil
	case "IDLE":
		// The receiver reports IDLE both before playback and after it finishes;
		// only "FINISHED" (or IDLE after having played) means the item ended.
		if strings.EqualFold(media.IdleReason, "FINISHED") || r.started {
			return stateEnded, nil
		}
		return statePlaying, nil
	default:
		return stateUnknown, nil
	}
}
