package tui

import "testing"

// esc must never quit the app: the bubbles list binds Quit to q AND esc and
// returns tea.Quit itself, so newList must disable that binding.
func TestNewListQuitDisabled(t *testing.T) {
	l := newList()
	if l.KeyMap.Quit.Enabled() {
		t.Error("list Quit binding must be disabled so esc cannot kill the app")
	}
}
