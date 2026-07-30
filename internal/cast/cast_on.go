//go:build cast

// This file is compiled only with the "cast" build tag (go build -tags cast).
// It marks the feature available. Phase 1 still returns the shared no-op Caster
// so no external dependency enters go.mod yet; Phase 3 replaces New with the
// real Chromecast + DLNA backends. UI code gates on Available, which is true
// here, so the cast key binding and popup become active in this build.
package cast

// Available reports whether this build includes cast support. True here.
const Available = true

// New returns the active Caster for the cast build.
//
// TODO(phase3): return a real Caster that fronts both a Chromecast (mDNS +
// go-chromecast) and a DLNA/UPnP (SSDP + AVTransport) backend, merging their
// discovered devices. For now it is the shared no-op so both build variants
// compile and are testable before any dependency is added.
func New() Caster { return noopCaster{} }
