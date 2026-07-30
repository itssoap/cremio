//go:build cast

package cast

import (
	"context"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

// fakeAV is an in-memory avTransport for testing the renderer's state logic.
type fakeAV struct {
	state  string
	uri    string
	meta   string
	played bool
}

func (f *fakeAV) SetAVTransportURI(_ uint32, uri, meta string) error {
	f.uri, f.meta, f.state = uri, meta, "STOPPED"
	return nil
}
func (f *fakeAV) Play(uint32, string) error { f.played = true; f.state = "PLAYING"; return nil }
func (f *fakeAV) Pause(uint32) error        { f.state = "PAUSED_PLAYBACK"; return nil }
func (f *fakeAV) Stop(uint32) error         { f.state = "STOPPED"; return nil }
func (f *fakeAV) GetTransportInfo(uint32) (string, string, string, error) {
	return f.state, "OK", "1", nil
}

// A just-loaded item reports STOPPED before playback begins; that must not be
// treated as "ended" (which would skip the item). Only STOPPED after a
// playing/paused state counts as ended.
func TestDLNAStateGuardAvoidsPrematureEnd(t *testing.T) {
	av := &fakeAV{state: "STOPPED"}
	r := &dlnaRenderer{tr: av}

	if st, _ := r.State(); st == stateEnded {
		t.Fatal("STOPPED before any playback must not be 'ended'")
	}
	av.state = "PLAYING"
	if st, _ := r.State(); st != statePlaying {
		t.Fatalf("PLAYING -> %v, want statePlaying", st)
	}
	av.state = "STOPPED"
	if st, _ := r.State(); st != stateEnded {
		t.Fatal("STOPPED after playing must be 'ended'")
	}
}

func TestDLNALoadResetsStartedGuard(t *testing.T) {
	av := &fakeAV{}
	r := &dlnaRenderer{tr: av}
	av.state = "PLAYING"
	_, _ = r.State() // started = true

	if err := r.Load(context.Background(), MediaItem{URL: "https://h/y.mkv"}); err != nil {
		t.Fatal(err)
	}
	av.state = "STOPPED" // renderer reports STOPPED right after load
	if st, _ := r.State(); st == stateEnded {
		t.Fatal("after Load, an immediate STOPPED must not be 'ended'")
	}
}

func TestGuessMimeFromURL(t *testing.T) {
	cases := map[string]string{
		"https://h/a.mkv":       "video/x-matroska",
		"https://h/a.mp4?token": "video/mp4",
		"https://h/a.avi":       "video/x-msvideo",
		"https://h/a.ts":        "video/mp2t",
		"https://h/noext":       "video/mp4",
	}
	for u, want := range cases {
		if got := guessMimeFromURL(u); got != want {
			t.Errorf("guessMimeFromURL(%q) = %q, want %q", u, got, want)
		}
	}
}

func TestDidlLiteIsWellFormedAndEscaped(t *testing.T) {
	x := didlLite(MediaItem{URL: "https://h/a b&c.mkv", Title: "A & B <x>"})
	dec := xml.NewDecoder(strings.NewReader(x))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("DIDL-Lite is not well-formed XML: %v\n%s", err, x)
		}
	}
	if !strings.Contains(x, "video/x-matroska") {
		t.Errorf("expected mkv mime in DIDL-Lite, got: %s", x)
	}
	if strings.Contains(x, "A & B") { // raw ampersand must have been escaped
		t.Errorf("title not XML-escaped: %s", x)
	}
}
