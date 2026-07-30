// Package cast provides an optional "cast to device" capability (Chromecast and
// DLNA/UPnP renderers on the local network). It is compiled in only when the
// binary is built with the "cast" build tag; the default build links a no-op
// implementation with no extra dependencies, so a cast-free binary is smaller
// and behaves exactly as if the feature did not exist.
//
// UI code must gate every cast interaction on the Available constant (defined
// per build tag in cast_on.go / cast_off.go). In the default build Available is
// false and New returns a no-op Caster, so cast key bindings do nothing.
//
// This file carries no build tag: the types, interfaces, and the shared no-op
// implementation are common to both builds. Phase 1 wires only this skeleton;
// the real Chromecast and DLNA backends arrive under //go:build cast later.
package cast

import (
	"context"
	"errors"
)

// ErrUnavailable is returned by cast operations in a build without cast support
// (or before a backend is wired up).
var ErrUnavailable = errors.New("cast: not available in this build")

// DeviceKind identifies the discovery/control protocol a device speaks.
type DeviceKind string

const (
	KindChromecast DeviceKind = "chromecast"
	KindDLNA       DeviceKind = "dlna"
)

// Device is a discovered cast target on the local network. Fields are populated
// from untrusted network responses and must be treated as such by the UI.
type Device struct {
	ID   string     // stable identity (mDNS instance / UPnP UUID)
	Name string     // friendly name shown in the picker
	Addr string     // host:port used to control the device
	Kind DeviceKind // chromecast | dlna
}

// MediaItem is one thing to play on a device. MimeType may be empty when the
// addon does not provide it; backends fall back to a sensible default.
type MediaItem struct {
	URL      string
	Title    string
	MimeType string
}

// SessionStatus is a snapshot of an active cast, for display in the popup.
type SessionStatus struct {
	State string // "idle" | "buffering" | "playing" | "paused"
	Index int    // current item index within the queue (0 for a single item)
	Title string
}

// Session controls one active cast on one device. Methods are best-effort and
// return an error if the device rejects or the session has ended.
type Session interface {
	Play() error
	Pause() error
	Stop() error
	Next() error
	Prev() error
	Status() (SessionStatus, error)
}

// Caster discovers devices and starts cast sessions. A single Caster may front
// multiple protocols (e.g. Chromecast and DLNA); Discover emits the merged,
// de-duplicated device list, so callers never care which protocol found a
// device.
type Caster interface {
	// Discover emits the current device list whenever it changes, until ctx is
	// cancelled, after which the channel is closed. Implementations should send
	// the full current slice (not deltas) so the UI can replace its list.
	Discover(ctx context.Context) (<-chan []Device, error)

	// Cast plays a single item on dev and returns a Session to control it.
	Cast(ctx context.Context, dev Device, item MediaItem) (Session, error)

	// CastQueue plays an ordered playlist on dev starting at startIndex.
	CastQueue(ctx context.Context, dev Device, items []MediaItem, startIndex int) (Session, error)

	// Close releases any discovery/control resources held by the Caster.
	Close() error
}

// noopCaster is the shared do-nothing implementation used by the default build
// and, in Phase 1, by the cast build too (until the real backends land). It
// never discovers a device and refuses to cast.
type noopCaster struct{}

func (noopCaster) Discover(ctx context.Context) (<-chan []Device, error) {
	ch := make(chan []Device)
	close(ch) // no devices, ever
	return ch, nil
}

func (noopCaster) Cast(context.Context, Device, MediaItem) (Session, error) {
	return nil, ErrUnavailable
}

func (noopCaster) CastQueue(context.Context, Device, []MediaItem, int) (Session, error) {
	return nil, ErrUnavailable
}

func (noopCaster) Close() error { return nil }
