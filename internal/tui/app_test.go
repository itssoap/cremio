package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/itssoap/cremio/internal/config"
	"github.com/itssoap/cremio/internal/history"
)

// esc must never quit the app: the bubbles list binds Quit to q AND esc and
// returns tea.Quit itself, so newList must disable that binding.
func TestNewListQuitDisabled(t *testing.T) {
	l := newList()
	if l.KeyMap.Quit.Enabled() {
		t.Error("list Quit binding must be disabled so esc cannot kill the app")
	}
}

// newTestApp builds an App with app-data env vars pointed at a temp dir, so the
// account tab can never read the developer's real auth.json.
func newTestApp(t *testing.T, hist *history.WatchHistory) App {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	if hist == nil {
		hist = &history.WatchHistory{}
	}
	return NewApp(&config.Config{Addons: []string{config.CinemetaURL}, SearchAddon: config.CinemetaURL}, hist, false)
}

func press(t *testing.T, a App, msg tea.Msg) (App, tea.Cmd) {
	t.Helper()
	m, cmd := a.Update(msg)
	return m.(App), cmd
}

var (
	keyEsc      = tea.KeyMsg{Type: tea.KeyEsc}
	keyTab      = tea.KeyMsg{Type: tea.KeyTab}
	keyShiftTab = tea.KeyMsg{Type: tea.KeyShiftTab}
	keyEnter    = tea.KeyMsg{Type: tea.KeyEnter}
)

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestEscOnRootAsksBeforeExiting(t *testing.T) {
	a := newTestApp(t, nil)

	a, cmd := press(t, a, keyEsc)
	if !a.confirmExit {
		t.Fatal("esc on a root screen should open the exit confirmation")
	}
	if isQuit(cmd) {
		t.Fatal("esc must not quit without confirmation")
	}
	if v := a.View(); !strings.Contains(v, "Exit cremio?") {
		t.Fatalf("popup not rendered:\n%s", v)
	}

	// The popup is modal: other keys must not leak to the screen underneath.
	a, _ = press(t, a, keyTab)
	if !a.confirmExit || a.screen != ScreenHome {
		t.Fatal("tab must be swallowed while the confirmation is open")
	}

	// n, esc and enter all stay.
	for _, k := range []tea.Msg{runeKey('n'), keyEsc, keyEnter} {
		a, _ = press(t, a, k)
		if a.confirmExit {
			t.Fatalf("%v should dismiss the confirmation", k)
		}
		a, _ = press(t, a, keyEsc) // reopen for the next key
	}

	// y exits.
	_, cmd = press(t, a, runeKey('y'))
	if !isQuit(cmd) {
		t.Fatal("y should quit")
	}
}

func TestEscStillGoesBackBeforeAskingToExit(t *testing.T) {
	a := newTestApp(t, nil)
	a.screen, a.prevScreen = ScreenDetail, ScreenSearch

	a, _ = press(t, a, keyEsc)
	if a.screen != ScreenSearch || a.confirmExit {
		t.Fatalf("esc on Detail should go back to the tab (screen=%v confirm=%v)", a.screen, a.confirmExit)
	}
	// Now at the root of Search: the next esc asks.
	a, _ = press(t, a, keyEsc)
	if !a.confirmExit {
		t.Fatal("esc on the Search root should ask to exit")
	}
}

func TestEscInsideInputDoesNotAskToExit(t *testing.T) {
	a := newTestApp(t, nil)
	a.screen = ScreenSearch
	a.search.inputFocused = true
	a.search.input.Focus()

	a, _ = press(t, a, keyEsc)
	if a.confirmExit {
		t.Fatal("esc while typing should only leave the input")
	}
	if a.search.inputFocused {
		t.Fatal("esc should have unfocused the search input")
	}
	// With the input released, esc is now at the root and asks.
	a, _ = press(t, a, keyEsc)
	if !a.confirmExit {
		t.Fatal("second esc should ask to exit")
	}
}

func TestTabAndShiftTabCycleAllTabs(t *testing.T) {
	a := newTestApp(t, nil)       // no history -> no History tab
	a.account.state = accLoggedIn // a focused login form consumes tab; see the form test
	want := []Screen{ScreenSearch, ScreenAddons, ScreenFilters, ScreenAccount, ScreenHome}
	for i, w := range want {
		a, _ = press(t, a, keyTab)
		if a.screen != w {
			t.Fatalf("tab #%d: screen = %v, want %v", i+1, a.screen, w)
		}
	}
	// Reverse from Home wraps to the last tab and walks back.
	back := []Screen{ScreenAccount, ScreenFilters, ScreenAddons, ScreenSearch, ScreenHome}
	for i, w := range back {
		a, _ = press(t, a, keyShiftTab)
		if a.screen != w {
			t.Fatalf("shift+tab #%d: screen = %v, want %v", i+1, a.screen, w)
		}
	}
}

func TestTabIncludesHistoryWhenPresent(t *testing.T) {
	hist := &history.WatchHistory{Movies: []history.WatchedMovie{{IDs: history.WatchIDs{IMDB: "tt1"}}}}
	a := newTestApp(t, hist)
	a, _ = press(t, a, keyTab)
	if a.screen != ScreenHistory {
		t.Fatalf("tab from Home with history = %v, want History", a.screen)
	}
	a, _ = press(t, a, keyShiftTab)
	if a.screen != ScreenHome {
		t.Fatalf("shift+tab from History = %v, want Home", a.screen)
	}
}

func TestTabWorksFromDetailAndStreams(t *testing.T) {
	// Both screens move relative to the tab they were opened from (Search).
	for _, drill := range []Screen{ScreenDetail, ScreenStreams} {
		a := newTestApp(t, nil)
		a.screen, a.prevScreen = drill, ScreenSearch
		a.streams.loading = true
		a, _ = press(t, a, keyTab)
		if a.screen != ScreenAddons {
			t.Errorf("tab from %v = %v, want Addons", drill, a.screen)
		}
		if drill == ScreenStreams && a.streams.loading {
			t.Error("leaving Streams should stop its load")
		}

		a = newTestApp(t, nil)
		a.screen, a.prevScreen = drill, ScreenSearch
		a, _ = press(t, a, keyShiftTab)
		if a.screen != ScreenHome {
			t.Errorf("shift+tab from %v = %v, want Home", drill, a.screen)
		}
	}
}

func TestTabLeavesInputsCleanly(t *testing.T) {
	// Search input focused: tab leaves and releases the field.
	a := newTestApp(t, nil)
	a.screen = ScreenSearch
	a.search.inputFocused = true
	a.search.input.Focus()
	a, _ = press(t, a, keyTab)
	if a.screen != ScreenAddons || a.search.inputFocused {
		t.Errorf("search: screen=%v inputFocused=%v", a.screen, a.search.inputFocused)
	}

	// Addons input active: tab leaves, cancels the add, and clears the text.
	a.addons.inputActive = true
	a.addons.input.SetValue("https://half-typed")
	a, _ = press(t, a, keyTab)
	if a.screen != ScreenFilters || a.addons.inputActive || a.addons.input.Value() != "" {
		t.Errorf("addons: screen=%v active=%v value=%q", a.screen, a.addons.inputActive, a.addons.input.Value())
	}

	// Filters mid-edit: tab leaves and reverts the field to its saved value.
	a.filters.editing = true
	a.filters.inputs[0].SetValue("half-typed")
	a, _ = press(t, a, keyTab)
	if a.screen != ScreenAccount || a.filters.editing || a.filters.inputs[0].Value() != "" {
		t.Errorf("filters: screen=%v editing=%v value=%q", a.screen, a.filters.editing, a.filters.inputs[0].Value())
	}
}

func TestAccountFormTabMovesFieldsThenLeaves(t *testing.T) {
	a := newTestApp(t, nil)
	a.screen = ScreenAccount
	if !a.account.editingInput() {
		t.Fatal("test setup: a logged-out account form should be focused")
	}
	last := len(a.account.inputs) - 1

	// tab walks the fields...
	for want := 1; want <= last; want++ {
		a, _ = press(t, a, keyTab)
		if a.screen != ScreenAccount || a.account.focus != want {
			t.Fatalf("tab should move to field %d (screen=%v focus=%d)", want, a.screen, a.account.focus)
		}
	}
	// ...and leaves from the last one (Account is the last tab, so it wraps to Home).
	a, _ = press(t, a, keyTab)
	if a.screen != ScreenHome {
		t.Fatalf("tab off the last field = %v, want Home", a.screen)
	}

	// Coming back, the form opens on its first field; shift+tab there leaves
	// backwards instead of trapping.
	a, _ = press(t, a, keyShiftTab) // Home -> Account
	if a.screen != ScreenAccount || a.account.focus != 0 {
		t.Fatalf("arriving on Account: screen=%v focus=%d, want Account/0", a.screen, a.account.focus)
	}
	a, _ = press(t, a, keyShiftTab)
	if a.screen != ScreenFilters {
		t.Fatalf("shift+tab off the first field = %v, want Filters", a.screen)
	}

	// esc leaves the form without asking to exit; tab then switches tabs.
	a, _ = press(t, a, keyTab) // Filters -> Account
	a, _ = press(t, a, keyEsc)
	if a.confirmExit || a.account.editingInput() {
		t.Fatal("esc in the form should just unfocus it")
	}
	a, _ = press(t, a, keyTab)
	if a.screen != ScreenHome {
		t.Fatalf("tab after leaving the form = %v, want Home", a.screen)
	}
}
