//go:build !cast

package cast

import "testing"

// In the default build the feature is compiled out.
func TestAvailableFalseByDefault(t *testing.T) {
	if Available {
		t.Fatal("Available must be false in the default (no-cast) build")
	}
}
