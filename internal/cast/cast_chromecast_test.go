//go:build cast

package cast

import (
	"context"
	"testing"

	castproto "github.com/vishen/go-chromecast/cast"
)

// fakeCastApp is an in-memory castApp for testing the Chromecast renderer's
// state mapping and the started guard without a real device.
type fakeCastApp struct {
	media  *castproto.Media
	paused bool
}

func (f *fakeCastApp) Load(string, int, string, bool, bool, bool) error { return nil }
func (f *fakeCastApp) Pause() error                                     { f.paused = true; return nil }
func (f *fakeCastApp) TogglePause() error                               { f.paused = !f.paused; return nil }
func (f *fakeCastApp) StopMedia() error                                 { return nil }
func (f *fakeCastApp) Update() error                                    { return nil }
func (f *fakeCastApp) Close(bool) error                                 { return nil }
func (f *fakeCastApp) Status() (*castproto.Application, *castproto.Media, *castproto.Volume) {
	return nil, f.media, nil
}

func mediaState(state, idle string) *castproto.Media {
	return &castproto.Media{PlayerState: state, IdleReason: idle}
}

func TestChromecastStateMapping(t *testing.T) {
	app := &fakeCastApp{}
	r := &chromecastRenderer{app: app}

	// IDLE before playback (not started) must not be "ended".
	app.media = mediaState("IDLE", "")
	if st, _ := r.State(); st == stateEnded {
		t.Fatal("IDLE before playback must not be 'ended'")
	}
	// Playing.
	app.media = mediaState("PLAYING", "")
	if st, _ := r.State(); st != statePlaying {
		t.Fatalf("PLAYING -> %v, want statePlaying", st)
	}
	// Paused.
	app.media = mediaState("PAUSED", "")
	if st, _ := r.State(); st != statePaused {
		t.Fatalf("PAUSED -> %v, want statePaused", st)
	}
	// Finished -> ended (advance the queue).
	app.media = mediaState("IDLE", "FINISHED")
	if st, _ := r.State(); st != stateEnded {
		t.Fatalf("IDLE/FINISHED -> %v, want stateEnded", st)
	}
}

func TestChromecastLoadResetsStartedGuard(t *testing.T) {
	app := &fakeCastApp{}
	r := &chromecastRenderer{app: app}

	app.media = mediaState("PLAYING", "")
	_, _ = r.State() // started = true

	if err := r.Load(context.Background(), MediaItem{URL: "https://h/y.mkv"}); err != nil {
		t.Fatal(err)
	}
	// Right after Load the receiver may still report IDLE with no reason; that
	// must not be treated as finished.
	app.media = mediaState("IDLE", "")
	if st, _ := r.State(); st == stateEnded {
		t.Fatal("after Load, an immediate IDLE must not be 'ended'")
	}
}

func TestChromecastPlayResumesOnlyWhenPaused(t *testing.T) {
	app := &fakeCastApp{media: mediaState("PAUSED", ""), paused: true}
	r := &chromecastRenderer{app: app}
	if err := r.Play(); err != nil {
		t.Fatal(err)
	}
	if app.paused {
		t.Fatal("Play() should resume a paused item (TogglePause)")
	}
	// When already playing, Play() is a no-op (does not toggle to paused).
	app.media = mediaState("PLAYING", "")
	if err := r.Play(); err != nil {
		t.Fatal(err)
	}
	if app.paused {
		t.Fatal("Play() while playing must not toggle to paused")
	}
}
