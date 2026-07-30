//go:build cast

package cast

import "testing"

// With -tags cast the feature is compiled in.
func TestAvailableTrueWithTag(t *testing.T) {
	if !Available {
		t.Fatal("Available must be true when built with -tags cast")
	}
}
