package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/itssoap/cremio/internal/account"
	"github.com/itssoap/cremio/internal/cast"
	"github.com/itssoap/cremio/internal/config"
	"github.com/itssoap/cremio/internal/history"
	"github.com/itssoap/cremio/internal/player"
	"github.com/itssoap/cremio/internal/stremio"
)

type Screen int

const (
	ScreenHome Screen = iota
	ScreenSearch
	ScreenAddons
	ScreenFilters
	ScreenAccount
	ScreenHistory
	ScreenDetail
	ScreenStreams
)

type App struct {
	screen     Screen
	prevScreen Screen
	width      int
	height     int
	incognito  bool

	client  *stremio.Client
	config  *config.Config
	history *history.WatchHistory
	dlMgr   *player.DownloadManager

	home       HomeModel
	search     SearchModel
	addons     AddonsModel
	filters    GlobalFiltersModel
	account    AccountModel
	historyTab HistoryModel
	detail     DetailModel
	streams    StreamsModel
	downloads  DownloadsModel
	cast       CastModel
}

func NewApp(cfg *config.Config, hist *history.WatchHistory, incognito bool) App {
	SetTheme(incognito)

	client := stremio.NewClient()
	detail := NewDetailModel(client, cfg)
	detail.history = hist
	detail.incognito = incognito

	parallel := cfg.DownloadParallel
	if parallel < 1 {
		parallel = 1
	}
	useAria2c := cfg.DownloadAria2c == nil || *cfg.DownloadAria2c
	dlMgr := player.NewDownloadManager(parallel, useAria2c)

	return App{
		screen:     ScreenHome,
		client:     client,
		config:     cfg,
		history:    hist,
		dlMgr:      dlMgr,
		incognito:  incognito,
		home:       NewHomeModel(client, cfg),
		search:     NewSearchModel(client, cfg),
		addons:     NewAddonsModel(client, cfg),
		filters:    NewGlobalFiltersModel(cfg),
		account:    NewAccountModel(cfg, incognito),
		historyTab: NewHistoryModel(hist, client, cfg),
		detail:     detail,
		streams:    NewStreamsModel(client, cfg),
		downloads:  NewDownloadsModel(dlMgr),
		cast:       NewCastModel(),
	}
}

func (a App) Init() tea.Cmd {
	cmds := []tea.Cmd{a.home.Init(), a.addons.Init(), a.search.Init()}
	// If a saved session is present (and not incognito), validate it and sync
	// on startup, in the background.
	if !a.incognito && a.account.Client().LoggedIn() {
		cmds = append(cmds, a.account.ValidateCmd())
	}
	return tea.Batch(cmds...)
}

// newList builds a list.Model with the built-in Quit binding disabled. The
// bubbles list binds Quit to both "q" and "esc" and returns tea.Quit itself,
// which would let esc kill the app. The app owns quitting (ctrl+c / q), so esc
// stays a back / clear-filter key only.
func newList() list.Model {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.KeyMap.Quit.SetEnabled(false)
	return l
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
		a.filters.SetSize(msg.Width-4, contentHeight)
		a.account.SetSize(msg.Width-4, contentHeight)
		a.historyTab.SetSize(msg.Width-4, contentHeight)
		a.detail.SetSize(msg.Width-4, contentHeight)
		a.streams.SetSize(msg.Width-4, contentHeight)
		a.downloads.SetSize(msg.Width-4, contentHeight)
		a.cast.SetSize(msg.Width-4, contentHeight)
		return a, nil

	case tea.KeyMsg:
		// Global quit. ctrl+c always quits; q quits from anywhere except while
		// typing into a text field. Handled before popup routing so q also quits
		// from the downloads popup and the release-group/episode selectors.
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
		if msg.String() == "q" && !a.isTyping() {
			return a, tea.Quit
		}
		// Global: toggle downloads popup
		if msg.String() == "D" && !a.isTyping() {
			a.downloads.Toggle()
			return a, nil
		}
		// Route all keys to downloads popup when visible
		if a.downloads.IsVisible() {
			var cmd tea.Cmd
			a.downloads, cmd = a.downloads.Update(msg)
			return a, cmd
		}
		// Cast popup (cast builds only): shift+c opens it on the streams screen.
		// In a default build cast.Available is a compile-time false, so this whole
		// branch is dead code and "C" does nothing, exactly as before.
		if cast.Available && msg.String() == "C" && a.screen == ScreenStreams && !a.isTyping() {
			if a.cast.IsVisible() {
				a.cast.Close()
				return a, nil
			}
			items, start, isPlaylist := a.streams.castTargets()
			cmd := a.cast.Open(items, start, isPlaylist)
			return a, cmd
		}
		if a.cast.IsVisible() {
			var cmd tea.Cmd
			a.cast, cmd = a.cast.Update(msg)
			return a, cmd
		}

		switch msg.String() {
		case "esc", "escape":
			// esc is a back key only; it never quits the app.
			if a.screen == ScreenStreams && (a.streams.filterActive || a.streams.rgSelectorActive || a.streams.rgEpSelectorActive) {
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
				a.streams.loading = false
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
			if a.screen == ScreenFilters && a.filters.editingInput() {
				break
			}
			if a.screen == ScreenAccount && a.account.editingInput() {
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
				a.screen = ScreenFilters
				return a, nil
			case ScreenFilters:
				a.screen = ScreenAccount
				return a, nil
			case ScreenAccount:
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
		a.streams.metaName = msg.MetaName
		a.streams.metaYear = msg.MetaYear
		a.streams.filterInput.SetValue("")
		a.streams.list.SetItems(nil)
		a.streams.resetDownloadState()
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
		a.streams.metaName = msg.MetaName
		a.streams.metaYear = msg.MetaYear
		a.streams.downloadMode = msg.DownloadMode
		a.streams.filterInput.SetValue("")
		a.streams.filterActive = true
		a.streams.list.SetItems(nil)
		a.streams.resetDownloadState()
		return a, a.streams.filterInput.Focus()

	case AddonAddedMsg:
		// Rebuild home/search so they pick up the new addon, then reload.
		return a, a.rebuildAddonViews()

	case AddonRemovedMsg:
		return a, a.rebuildAddonViews()

	case mpvLaunchedMsg:
		if a.history != nil && msg.videoID != "" && !a.incognito {
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

	case enqueueDownloadsMsg:
		// Batch download: enqueue selected episodes from chosen release group
		downloadDir := a.config.ResolveDownloadDir()
		count := a.streams.enqueueBatchDownload(a.dlMgr, a.streams.rgSelectedGroup, downloadDir)
		a.streams.resetDownloadState()
		a.streams.downloadMsg = fmt.Sprintf("Queued %d file(s) (press D to view downloads)", count)
		return a, a.processDownloadQueue()

	case enqueueSingleDownloadMsg:
		downloadDir := a.config.ResolveDownloadDir()
		a.streams.enqueueSingleDownload(a.dlMgr, msg.item, downloadDir)
		a.streams.downloadMsg = "Queued download (press D to view)"
		return a, a.processDownloadQueue()

	case downloadTickMsg:
		// Process next queued job if capacity available
		return a, a.processDownloadQueue()

	case downloadJobDoneMsg:
		// Job finished, try processing next
		return a, a.processDownloadQueue()

	case castMsg:
		// Discovery / cast-session updates for the cast popup.
		var cmd tea.Cmd
		a.cast, cmd = a.cast.Update(msg)
		return a, cmd

	case accountLoginMsg:
		if a.account.ApplyLogin(msg) {
			a.account.SetStatus("Syncing...")
			return a, a.accountSyncCmd()
		}
		return a, nil

	case accountLogoutMsg:
		a.account.ApplyLogout()
		return a, nil

	case accountSyncRequestMsg:
		if a.incognito {
			return a, nil
		}
		return a, a.accountSyncCmd()

	case accountSyncResultMsg:
		var cmd tea.Cmd
		var parts []string
		// Apply whatever was fetched, even on a partial error (e.g. addons
		// succeeded but the library call failed).
		if msg.addonURLs != nil {
			merged, added := account.MergeAddonURLs(a.config.Addons, msg.addonURLs)
			if added > 0 {
				a.config.Addons = merged
				_ = a.config.Save()
				cmd = a.rebuildAddonViews()
			}
			parts = append(parts, fmt.Sprintf("%d addon(s) added", added))
		}
		if msg.library != nil && a.history != nil && !a.incognito {
			n := account.ApplyLibraryToHistory(a.history, msg.library)
			if n > 0 {
				_ = a.history.Save()
				a.historyTab.Refresh()
			}
			parts = append(parts, fmt.Sprintf("%d history item(s)", n))
		}
		if msg.err != nil {
			a.account.SetStatus("Sync incomplete: " + msg.err.Error())
		} else {
			a.account.SetLastSync(time.Now())
			a.account.SetStatus("Synced: " + strings.Join(parts, ", "))
		}
		return a, cmd
	}

	var cmd tea.Cmd
	switch a.screen {
	case ScreenHome:
		a.home, cmd = a.home.Update(msg)
	case ScreenSearch:
		a.search, cmd = a.search.Update(msg)
	case ScreenAddons:
		a.addons, cmd = a.addons.Update(msg)
	case ScreenFilters:
		a.filters, cmd = a.filters.Update(msg)
	case ScreenAccount:
		a.account, cmd = a.account.Update(msg)
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
	case ScreenFilters:
		content = a.filters.View()
	case ScreenAccount:
		content = a.account.View()
	case ScreenHistory:
		content = a.historyTab.View()
	case ScreenDetail:
		content = a.detail.View()
	case ScreenStreams:
		content = a.streams.View()
	}

	// Download indicator in status
	dlStatus := ""
	if a.dlMgr.ActiveCount() > 0 || a.dlMgr.QueuedCount() > 0 {
		dlStatus = fmt.Sprintf(" [⬇ %d active, %d queued]", a.dlMgr.ActiveCount(), a.dlMgr.QueuedCount())
	}

	view := AppStyle.Render(lipgloss.JoinVertical(lipgloss.Left, tabs+dlStatus, content))

	// Overlay downloads popup
	if a.downloads.IsVisible() {
		view += "\n" + a.downloads.View()
	}
	// Overlay cast popup
	if a.cast.IsVisible() {
		view += "\n" + a.cast.View()
	}

	return view
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

// isTyping returns true if any text input is currently focused.
func (a App) isTyping() bool {
	if a.screen == ScreenAddons && a.addons.inputActive {
		return true
	}
	if a.screen == ScreenSearch && a.search.inputFocused {
		return true
	}
	if a.screen == ScreenStreams && a.streams.filterActive {
		return true
	}
	if a.screen == ScreenFilters && a.filters.editingInput() {
		return true
	}
	if a.screen == ScreenAccount && a.account.editingInput() {
		return true
	}
	return a.listFiltering()
}

// processDownloadQueue starts as many queued jobs as capacity allows and keeps
// a UI refresh tick running while anything is active or queued.
func (a App) processDownloadQueue() tea.Cmd {
	queued := a.dlMgr.QueuedCount()
	active := a.dlMgr.ActiveCount()
	if queued == 0 && active == 0 {
		return nil
	}

	var cmds []tea.Cmd
	slots := a.dlMgr.FreeSlots()
	starts := slots
	if queued < starts {
		starts = queued
	}
	for i := 0; i < starts; i++ {
		mgr := a.dlMgr
		cmds = append(cmds, func() tea.Msg {
			_, _ = mgr.ProcessQueue(context.Background())
			return downloadJobDoneMsg{}
		})
	}
	// Refresh tick so progress and finished jobs surface promptly.
	cmds = append(cmds, tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return downloadTickMsg{}
	}))
	return tea.Batch(cmds...)
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
	tabs = append(tabs, tab{"Search", ScreenSearch}, tab{"Addons", ScreenAddons}, tab{"Filters", ScreenFilters}, tab{"Account", ScreenAccount})

	var rendered []string
	if a.incognito {
		// Reverse-video pill instead of an emoji: renders on every terminal
		// (including legacy Windows conhost) where astral-plane glyphs tofu.
		badge := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1e1e2e")). // base (dark) text
			Background(lipgloss.Color("#cba6f7")). // mauve fill
			Padding(0, 1).
			Render("INCOGNITO")
		rendered = append(rendered, badge)
	}
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
	ID       string
	Type     string
	MetaName string
	MetaYear string
}

type NavigateToAllStreamsMsg struct {
	Videos       []stremio.Video
	Type         string
	MetaName     string
	MetaYear     string
	DownloadMode bool
}

type AddonAddedMsg struct{}
type AddonRemovedMsg struct{}

// accountSyncResultMsg carries the data fetched by a background account sync.
// The data is applied to config/history on the main Update loop (not in the
// command goroutine) to avoid racing the shared config/history state.
type accountSyncResultMsg struct {
	addonURLs []string              // fetched transport URLs, nil when addons not synced
	library   []account.LibraryItem // fetched library, nil when history not synced
	err       error
}

// accountSyncCmd fetches the addon collection and/or library in the background.
func (a App) accountSyncCmd() tea.Cmd {
	client := a.account.Client()
	syncAddons := a.config.Account.SyncAddons
	syncHistory := a.config.Account.SyncHistory && !a.incognito
	return func() tea.Msg {
		if !client.LoggedIn() {
			return accountSyncResultMsg{err: fmt.Errorf("not logged in")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		var res accountSyncResultMsg
		if syncAddons {
			addons, err := client.AddonCollection(ctx)
			if err != nil {
				return accountSyncResultMsg{err: err}
			}
			res.addonURLs = account.AddonURLs(addons)
		}
		if syncHistory {
			items, err := client.Library(ctx)
			if err != nil {
				res.err = err
				return res
			}
			res.library = items
		}
		return res
	}
}
