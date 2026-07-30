//go:build !cast

// This file is compiled into the DEFAULT build (no "cast" build tag). It links
// no cast dependencies and makes the feature a no-op: Available is false and
// New returns the shared no-op Caster, so cast key bindings do nothing and the
// binary stays small.
package cast

// Available reports whether this build includes cast support. False here.
const Available = false

// New returns a no-op Caster in the default build.
func New() Caster { return noopCaster{} }
