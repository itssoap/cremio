package tui

import (
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/itssoap/cremio/internal/config"
	"github.com/itssoap/cremio/internal/history"
	"github.com/itssoap/cremio/internal/stremio"
)

type Screen int

const (
	ScreenHome Screen = iota
	ScreenSearch
	ScreenAddons
	ScreenHistory
	ScreenDetail
	ScreenStreams
)

type App struct {
	screen     Screen
	prevScreen Screen
	width      int
	height     int

	client  *stremio.Client
	config  *config.Config
	history *history.WatchHistory

	home       HomeModel
	search     SearchModel
	addons     AddonsModel
	historyTab HistoryModel
	detail     DetailModel
	streams    StreamsModel
}

func NewApp(cfg *config.Config, hist *history.WatchHistory) App {
	client := stremio.NewClient()
	detail := NewDetailModel(client, cfg)
	detail.history = hist
	return App{
		screen:     ScreenHome,
		client:     client,
		config:     cfg,
		history:    hist,
		home:       NewHomeModel(client, cfg),
		search:     NewSearchModel(client, cfg),
		addons:     NewAddonsModel(client, cfg),
		historyTab: NewHistoryModel(hist, client, cfg),
		detail:     detail,
		streams:    NewStreamsModel(client, cfg),
	}
}

func (a App) Init() tea.Cmd {
	return tea.Batch(a.home.Init(), a.addons.Init(), a.search.Init())
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		contentHeight := msg.Height - 4 // tab bar + padding
		a.home.SetSize(msg.Width-4, contentHeight)
		a.search.SetSize(msg.Width-4, contentHeight)
		a.addons.SetSize(msg.Width-4, contentHeight)
		a.historyTab.SetSize(msg.Width-4, contentHeight)
		a.detail.SetSize(msg.Width-4, contentHeight)
		a.streams.SetSize(msg.Width-4, contentHeight)
		return a, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if a.screen == ScreenAddons && a.addons.inputActive {
				break
			}
			if a.screen == ScreenSearch && a.search.inputFocused {
				break
			}
			if a.screen == ScreenStreams && a.streams.filterActive {
				break
			}
			// Don't treat "q" as quit while typing in a list's built-in filter.
			if msg.String() == "q" && a.listFiltering() {
				break
			}
			return a, tea.Quit
		case "esc", "escape":
			if a.screen == ScreenStreams && a.streams.filterActive {
				break
			}
			if a.screen == ScreenDetail && a.detail.viewingEpisodes {
				break
			}
			if a.screen == ScreenDetail {
				a.screen = a.prevScreen
				return a, nil
			}
			if a.screen == ScreenStreams {
				a.screen = ScreenDetail
				return a, nil
			}
		case "tab":
			// Don't switch tabs while typing in a list's built-in filter.
			if a.listFiltering() {
				break
			}
			if a.screen == ScreenDetail || a.screen == ScreenStreams {
				// Allow tab to escape back from error states
				if (a.screen == ScreenDetail && a.detail.err != nil) || (a.screen == ScreenStreams && a.streams.err != nil) {
					a.screen = a.prevScreen
					return a, nil
				}
				break
			}
			if a.screen == ScreenAddons && a.addons.inputActive {
				break
			}
			if a.screen == ScreenSearch && a.search.inputFocused {
				break
			}
			switch a.screen {
			case ScreenHome:
				if a.hasHistory() {
					a.screen = ScreenHistory
					a.historyTab.Refresh()
					return a, a.historyTab.FetchMissingTitles()
				}
				a.screen = ScreenSearch
				if a.config.AutoFocusSearch {
					a.search.inputFocused = true
					return a, a.search.input.Focus()
				}
				return a, nil
			case ScreenHistory:
				a.screen = ScreenSearch
				if a.config.AutoFocusSearch {
					a.search.inputFocused = true
					return a, a.search.input.Focus()
				}
				return a, nil
			case ScreenSearch:
				a.screen = ScreenAddons
				return a, nil
			case ScreenAddons:
				a.screen = ScreenHome
				return a, a.home.Init()
			}
			return a, nil
		}

	case NavigateToDetailMsg:
		a.prevScreen = a.screen
		a.screen = ScreenDetail
		a.detail.loading = true
		a.detail.meta = nil
		a.detail.err = nil
		a.detail.requestedID = history.ExtractIMDBID(msg.ID)
		return a, tea.Batch(a.detail.spinner.Tick, a.detail.LoadMeta(msg))

	case NavigateToStreamsMsg:
		a.screen = ScreenStreams
		a.streams.loading = true
		a.streams.err = nil
		a.streams.allItems = nil
		a.streams.pendingVideos = nil
		a.streams.pendingType = ""
		a.streams.contentID = msg.ID
		a.streams.contentType = msg.Type
		a.streams.filterInput.SetValue("")
		a.streams.list.SetItems(nil)
		return a, tea.Batch(a.streams.spinner.Tick, a.streams.LoadStreams(msg))

	case NavigateToAllStreamsMsg:
		a.screen = ScreenStreams
		a.streams.loading = false
		a.streams.err = nil
		a.streams.allItems = nil
		a.streams.pendingVideos = msg.Videos
		a.streams.pendingType = msg.Type
		a.streams.contentID = ""
		a.streams.contentType = msg.Type
		a.streams.filterInput.SetValue("")
		a.streams.filterActive = true
		a.streams.list.SetItems(nil)
		return a, a.streams.filterInput.Focus()

	case AddonAddedMsg:
		// Rebuild home/search so they pick up the new addon, then reload.
		return a, a.rebuildAddonViews()

	case AddonRemovedMsg:
		return a, a.rebuildAddonViews()

	case mpvLaunchedMsg:
		if a.history != nil && msg.videoID != "" {
			// Use the detail model's history ID when available so that
			// addons using different ID schemes (e.g. tvdb:XXXX in meta
			// but tt-prefixed video IDs) stay consistent.
			imdbID := a.detail.historyID()
			if imdbID == "" {
				imdbID = history.ExtractIMDBID(msg.videoID)
			}
			if msg.videoType == "movie" {
				if !a.history.IsMovieWatched(imdbID) {
					a.history.ToggleMovie(imdbID)
					_ = a.history.Save()
				}
			} else if msg.videoType == "series" {
				season, episode := history.ParseEpisodeID(msg.videoID)
				if season > 0 && !a.history.IsEpisodeWatched(imdbID, season, episode) {
					a.history.ToggleEpisode(imdbID, season, episode)
					_ = a.history.Save()
				}
			}
			// Refresh detail view watched indicators
			if a.detail.meta != nil && a.detail.viewingEpisodes {
				a.detail.showEpisodesForSeason(a.detail.selectedSeason)
			}
			a.historyTab.Refresh()
		}
		// Pass to streams for launch status update
		var cmd tea.Cmd
		a.streams, cmd = a.streams.Update(msg)
		return a, cmd

	case addonsRefreshedMsg:
		a.addons, _ = a.addons.Update(msg)
		return a, nil

	case historyTitleMsg:
		a.historyTab, _ = a.historyTab.Update(msg)
		return a, nil

	case searchAddonNameMsg:
		a.search, _ = a.search.Update(msg)
		return a, nil
	}

	var cmd tea.Cmd
	switch a.screen {
	case ScreenHome:
		a.home, cmd = a.home.Update(msg)
	case ScreenSearch:
		a.search, cmd = a.search.Update(msg)
	case ScreenAddons:
		a.addons, cmd = a.addons.Update(msg)
	case ScreenHistory:
		a.historyTab, cmd = a.historyTab.Update(msg)
	case ScreenDetail:
		if keyMsg, ok := msg.(tea.KeyMsg); ok && (keyMsg.String() == "esc" || keyMsg.String() == "escape") {
			if a.detail.viewingEpisodes {
				// Let detail model handle going back to season list
				a.detail, cmd = a.detail.Update(msg)
				return a, cmd
			}
			a.screen = a.prevScreen
			return a, nil
		}
		a.detail, cmd = a.detail.Update(msg)
	case ScreenStreams:
		a.streams, cmd = a.streams.Update(msg)
	}
	return a, cmd
}

func (a App) View() string {
	tabs := a.renderTabs()
	var content string
	switch a.screen {
	case ScreenHome:
		content = a.home.View()
	case ScreenSearch:
		content = a.search.View()
	case ScreenAddons:
		content = a.addons.View()
	case ScreenHistory:
		content = a.historyTab.View()
	case ScreenDetail:
		content = a.detail.View()
	case ScreenStreams:
		content = a.streams.View()
	}
	return AppStyle.Render(lipgloss.JoinVertical(lipgloss.Left, tabs, content))
}

func (a App) hasHistory() bool {
	return a.history != nil && (len(a.history.Movies) > 0 || len(a.history.Shows) > 0)
}

// rebuildAddonViews recreates the Home and Search models so they reflect the
// current addon list, restores their size, and returns the commands that
// trigger their initial reload (catalog fetch / search-addon name lookup).
func (a *App) rebuildAddonViews() tea.Cmd {
	a.home = NewHomeModel(a.client, a.config)
	a.search = NewSearchModel(a.client, a.config)
	if a.width > 0 && a.height > 0 {
		contentHeight := a.height - 4
		a.home.SetSize(a.width-4, contentHeight)
		a.search.SetSize(a.width-4, contentHeight)
	}
	return tea.Batch(a.home.Init(), a.search.Init())
}

// listFiltering reports whether the active tab's list is currently capturing
// keystrokes for its built-in filter, so global keys like "q"/"tab" should be
// passed through to the list instead of being handled at the app level.
func (a App) listFiltering() bool {
	switch a.screen {
	case ScreenHome:
		return a.home.list.FilterState() == list.Filtering
	case ScreenHistory:
		return a.historyTab.list.FilterState() == list.Filtering
	}
	return false
}

func (a App) renderTabs() string {
	type tab struct {
		name   string
		screen Screen
	}
	tabs := []tab{{"Home", ScreenHome}}
	if a.hasHistory() {
		tabs = append(tabs, tab{"History", ScreenHistory})
	}
	tabs = append(tabs, tab{"Search", ScreenSearch}, tab{"Addons", ScreenAddons})

	var rendered []string
	for _, t := range tabs {
		if t.screen == a.screen || (a.screen == ScreenDetail && t.screen == a.prevScreen) || (a.screen == ScreenStreams && t.screen == a.prevScreen) {
			rendered = append(rendered, TabActiveStyle.Render(t.name))
		} else {
			rendered = append(rendered, TabInactiveStyle.Render(t.name))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
}

// Navigation messages
type NavigateToDetailMsg struct {
	ID      string
	Type    string
	BaseURL string
}

type NavigateToStreamsMsg struct {
	ID   string
	Type string
}

type NavigateToAllStreamsMsg struct {
	Videos []stremio.Video
	Type   string
}

type AddonAddedMsg struct{}
type AddonRemovedMsg struct{}
