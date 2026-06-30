package tui

import (
	"context"
	"fmt"
	"sort"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/itssoap/cremio/internal/config"
	"github.com/itssoap/cremio/internal/history"
	"github.com/itssoap/cremio/internal/stremio"
)

type historyTitleMsg struct {
	imdbID string
	title  string
}

// historyEntry is a flat list item representing one watched movie or show.
type historyEntry struct {
	imdbID    string
	entryType string // "movie" or "series"
	watchedAt string // most recent watched_at timestamp
	title     string // empty until fetched
}

func (h historyEntry) Title() string {
	badge := "[movie]"
	if h.entryType == "series" {
		badge = "[show] "
	}
	display := h.imdbID
	if h.title != "" {
		display = fmt.Sprintf("%s (%s)", h.title, h.imdbID)
	}
	return fmt.Sprintf("%s  %s", badge, display)
}

func (h historyEntry) Description() string {
	if h.watchedAt != "" {
		return "Last watched: " + h.watchedAt
	}
	return ""
}

func (h historyEntry) FilterValue() string {
	if h.title != "" {
		return h.title + " " + h.imdbID
	}
	return h.imdbID
}

// HistoryModel is the History tab backed by an in-memory WatchHistory.
type HistoryModel struct {
	list    list.Model
	history *history.WatchHistory
	client  *stremio.Client
	config  *config.Config
	titles  map[string]string // imdbID -> title cache
	width   int
	height  int
}

func NewHistoryModel(hist *history.WatchHistory, client *stremio.Client, cfg *config.Config) HistoryModel {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Watch History"
	l.SetShowHelp(false)
	l.SetFilteringEnabled(true)

	m := HistoryModel{
		list:    l,
		history: hist,
		client:  client,
		config:  cfg,
		titles:  make(map[string]string),
	}
	m.Refresh()
	return m
}

func (m *HistoryModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.list.SetSize(w, h)
}

// Refresh rebuilds the list from the current WatchHistory and the title cache.
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
			title:     m.titles[mv.IDs.IMDB],
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
			title:     m.titles[sh.IDs.IMDB],
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

// FetchMissingTitles fires async meta fetches for every history entry whose
// title is not yet in the cache. Safe to call multiple times -- already-cached
// entries are skipped.
func (m *HistoryModel) FetchMissingTitles() tea.Cmd {
	type entry struct {
		imdbID      string
		contentType string
	}
	seen := make(map[string]bool)
	var pending []entry

	collect := func(imdbID, ct string) {
		if seen[imdbID] || m.titles[imdbID] != "" {
			return
		}
		seen[imdbID] = true
		pending = append(pending, entry{imdbID, ct})
	}

	if m.history != nil {
		for _, mv := range m.history.Movies {
			collect(mv.IDs.IMDB, "movie")
		}
		for _, sh := range m.history.Shows {
			collect(sh.IDs.IMDB, "series")
		}
	}

	addons := append([]string(nil), m.config.Addons...)
	cmds := make([]tea.Cmd, 0, len(pending))
	for _, e := range pending {
		id, ct := e.imdbID, e.contentType
		cmds = append(cmds, func() tea.Msg {
			ctx := context.Background()
			for _, addonURL := range addons {
				resp, err := m.client.FetchMeta(ctx, addonURL, ct, id)
				if err != nil || resp.Meta.Name == "" {
					continue
				}
				if resp.Meta.ID != "" && resp.Meta.ID != id {
					continue
				}
				return historyTitleMsg{imdbID: id, title: resp.Meta.Name}
			}
			return nil
		})
	}
	return tea.Batch(cmds...)
}

func (m HistoryModel) Init() tea.Cmd { return nil }

func (m HistoryModel) Update(msg tea.Msg) (HistoryModel, tea.Cmd) {
	switch msg := msg.(type) {
	case historyTitleMsg:
		m.titles[msg.imdbID] = msg.title
		m.Refresh()
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "enter" {
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
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m HistoryModel) View() string {
	if m.history == nil || (len(m.history.Movies) == 0 && len(m.history.Shows) == 0) {
		return SubtitleStyle.Render("No watch history yet. Play something and it will appear here.")
	}
	return m.list.View() + "\n" + HelpStyle.Render("enter: open • tab: switch tab • D: downloads • q: quit")
}
