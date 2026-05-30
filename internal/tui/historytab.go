package tui

import (
	"fmt"
	"sort"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/itssoap/cremio/internal/history"
)

// historyEntry is a flat list item representing one watched movie or show.
type historyEntry struct {
	imdbID    string
	entryType string // "movie" or "series"
	watchedAt string // most recent watched_at timestamp
}

func (h historyEntry) Title() string {
	badge := "[movie]"
	if h.entryType == "series" {
		badge = "[show] "
	}
	return fmt.Sprintf("%s  %s", badge, h.imdbID)
}

func (h historyEntry) Description() string {
	if h.watchedAt != "" {
		return "Last watched: " + h.watchedAt
	}
	return ""
}

func (h historyEntry) FilterValue() string { return h.imdbID }

// HistoryModel is the read-only History tab. It renders directly from the
// already-loaded *history.WatchHistory in memory -- no extra HTTP calls or
// disk reads on the tab itself.
type HistoryModel struct {
	list    list.Model
	history *history.WatchHistory
	width   int
	height  int
}

func NewHistoryModel(hist *history.WatchHistory) HistoryModel {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Watch History"
	l.SetShowHelp(false)
	l.SetFilteringEnabled(true)

	m := HistoryModel{
		list:    l,
		history: hist,
	}
	m.Refresh()
	return m
}

func (m *HistoryModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.list.SetSize(w, h)
}

// Refresh rebuilds the list from the current WatchHistory contents.
// It is a pure in-memory operation -- no goroutines, no disk I/O.
func (m *HistoryModel) Refresh() {
	if m.history == nil {
		m.list.SetItems(nil)
		return
	}

	var entries []historyEntry

	for _, mv := range m.history.Movies {
		entries = append(entries, historyEntry{
			imdbID:    mv.IDs.IMDB,
			entryType: "movie",
			watchedAt: mv.WatchedAt,
		})
	}

	for _, sh := range m.history.Shows {
		latest := ""
		for _, sn := range sh.Seasons {
			for _, ep := range sn.Episodes {
				if ep.WatchedAt > latest {
					latest = ep.WatchedAt
				}
			}
		}
		entries = append(entries, historyEntry{
			imdbID:    sh.IDs.IMDB,
			entryType: "series",
			watchedAt: latest,
		})
	}

	// Most recently watched first.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].watchedAt > entries[j].watchedAt
	})

	items := make([]list.Item, len(entries))
	for i, e := range entries {
		items[i] = e
	}
	m.list.SetItems(items)
}

func (m HistoryModel) Init() tea.Cmd { return nil }

func (m HistoryModel) Update(msg tea.Msg) (HistoryModel, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "enter" {
		if item, ok := m.list.SelectedItem().(historyEntry); ok {
			return m, func() tea.Msg {
				return NavigateToDetailMsg{
					ID:      item.imdbID,
					Type:    item.entryType,
					BaseURL: "",
				}
			}
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m HistoryModel) View() string {
	if m.history == nil || (len(m.history.Movies) == 0 && len(m.history.Shows) == 0) {
		return SubtitleStyle.Render("No watch history yet. Play something and it will appear here.")
	}
	return m.list.View()
}
