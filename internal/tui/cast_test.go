package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/itssoap/cremio/internal/cast"
	"github.com/itssoap/cremio/internal/config"
	"github.com/itssoap/cremio/internal/stremio"
)

// fakeCaster is a deterministic Caster for TUI tests: it discovers nothing and
// refuses to cast, so CastModel tests never touch the real network backend
// (which the cast build would otherwise start).
type fakeCaster struct{}

func (fakeCaster) Discover(context.Context) (<-chan []cast.Device, error) {
	ch := make(chan []cast.Device)
	close(ch)
	return ch, nil
}
func (fakeCaster) Cast(context.Context, cast.Device, cast.MediaItem) (cast.Session, error) {
	return nil, errors.New("unavailable")
}
func (fakeCaster) CastQueue(context.Context, cast.Device, []cast.MediaItem, int) (cast.Session, error) {
	return nil, errors.New("unavailable")
}
func (fakeCaster) Close() error { return nil }

func TestCastModelFlow(t *testing.T) {
	m := newCastModelWith(fakeCaster{})
	m.SetSize(80, 24)

	// Open with a castable item: popup shows, discovery starts.
	cmd := m.Open([]cast.MediaItem{{URL: "https://host/y.mkv", Title: "Y"}}, 0, false)
	if !m.IsVisible() {
		t.Fatal("popup should be visible after Open")
	}
	if cmd == nil {
		t.Fatal("Open should return a discovery command")
	}
	// The stub caster closes discovery immediately (no devices).
	if _, ok := cmd().(castDiscoveryClosedMsg); !ok {
		t.Fatalf("stub discovery should close, got %T", cmd())
	}

	// A discovered device gets recorded.
	m, _ = m.Update(castDevicesMsg{devices: []cast.Device{{ID: "1", Name: "Living Room TV", Kind: cast.KindChromecast}}})
	if len(m.devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(m.devices))
	}

	// enter tries to cast; the stub backend refuses with ErrUnavailable.
	_, castCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if castCmd == nil {
		t.Fatal("enter on a device should return a cast command")
	}
	if _, ok := castCmd().(castErrMsg); !ok {
		t.Fatalf("stub cast should return castErrMsg, got %T", castCmd())
	}

	// esc closes the popup.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.IsVisible() {
		t.Fatal("esc should close the popup")
	}
}

func TestCastModelUncastableSelection(t *testing.T) {
	m := newCastModelWith(fakeCaster{})
	m.SetSize(80, 24)
	m.Open(nil, 0, false) // nothing castable
	if !strings.Contains(m.status, "not castable") {
		t.Fatalf("status should explain non-castable selection, got %q", m.status)
	}
	// enter with no items surfaces an error rather than casting.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.errMsg == "" {
		t.Fatal("enter with nothing castable should set an error")
	}
}

func TestCastTargetsHTTPGate(t *testing.T) {
	m := NewStreamsModel(stremio.NewClient(), &config.Config{})
	m.contentType = "movie"
	m.metaName = "Movie"

	// http(s) single stream is castable.
	m.list.SetItems([]list.Item{streamItem{stream: stremio.Stream{URL: "https://host/movie.mkv"}}})
	items, _, isPlaylist := m.castTargets()
	if isPlaylist || len(items) != 1 || items[0].URL != "https://host/movie.mkv" {
		t.Fatalf("expected one http item, got %+v (playlist=%v)", items, isPlaylist)
	}

	// A magnet/torrent stream is not castable.
	m.list.SetItems([]list.Item{streamItem{stream: stremio.Stream{InfoHash: "abc123"}}})
	items, _, _ = m.castTargets()
	if len(items) != 0 {
		t.Fatalf("magnet stream must not be castable, got %+v", items)
	}
}
