//go:build cast

// This file is compiled only with the "cast" build tag (go build -tags cast).
// It marks the feature available. Phase 1 still returns the shared no-op Caster
// so no external dependency enters go.mod yet; Phase 3 replaces New with the
// real Chromecast + DLNA backends. UI code gates on Available, which is true
// here, so the cast key binding and popup become active in this build.
package cast

// Available reports whether this build includes cast support. True here.
const Available = true

// New returns the active Caster for the cast build: an aggregate over the
// registered protocol backends. In sub-phase 3a no backends are registered yet
// (so discovery yields nothing, exactly like the default build); 3b adds the
// Chromecast backend and 3c the DLNA backend.
func New() Caster {
	return newAggregateCaster(backends()...)
}

// backends returns the protocol backends compiled into this build. DLNA is
// first (priority: native audio/subtitle switching via the TV remote); the
// Chromecast backend is appended in sub-phase 3b.
func backends() []backend {
	return []backend{
		newDLNABackend(),
	}
}
