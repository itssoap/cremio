package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/itssoap/cremio/internal/config"
	"github.com/itssoap/cremio/internal/history"
	"github.com/itssoap/cremio/internal/stremio"
)

type videoItem struct {
	video   stremio.Video
	watched bool
}

func (v videoItem) Title() string {
	prefix := ""
	if v.watched {
		prefix = "✓ "
	}
	if v.video.Season > 0 {
		return fmt.Sprintf("%sS%02dE%02d - %s", prefix, v.video.Season, v.video.Episode, v.video.DisplayTitle())
	}
	return prefix + v.video.DisplayTitle()
}
func (v videoItem) Description() string { return v.video.Overview }
func (v videoItem) FilterValue() string { return v.video.DisplayTitle() }

type seasonItem struct {
	season       int
	episodeCount int
	watched      bool
}

func (s seasonItem) Title() string {
	prefix := ""
	if s.watched {
		prefix = "✓ "
	}
	return fmt.Sprintf("%sSeason %d", prefix, s.season)
}
func (s seasonItem) Description() string { return fmt.Sprintf("%d episodes", s.episodeCount) }
func (s seasonItem) FilterValue() string { return fmt.Sprintf("Season %d", s.season) }

type DetailModel struct {
	meta            *stremio.Meta
	list            list.Model
	spinner         spinner.Model
	client          *stremio.Client
	config          *config.Config
	history         *history.WatchHistory
	loading         bool
	err             error
	saveErr         error
	width           int
	height          int
	contentType     string
	viewingEpisodes bool
	selectedSeason  int
	requestedID     string // the ID used to navigate here; used for history lookups
}

type metaLoadedMsg struct {
	meta *stremio.Meta
}
type metaErrorMsg struct {
	err error
}

func NewDetailModel(client *stremio.Client, cfg *config.Config) DetailModel {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Episodes"
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("170"))

	return DetailModel{
		list:    l,
		spinner: s,
		client:  client,
		config:  cfg,
	}
}

func (m *DetailModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.list.SetSize(w, h-10)
}

func (m *DetailModel) historyID() string {
	if m.requestedID != "" {
		return m.requestedID
	}
	if m.meta != nil && m.meta.IMDBID != "" {
		return m.meta.IMDBID
	}
	if m.meta != nil {
		return history.ExtractIMDBID(m.meta.ID)
	}
	return ""
}

func (m *DetailModel) showSeasons() {
	imdbID := m.historyID()
	seasonMap := make(map[int]int)
	for _, v := range m.meta.Videos {
		seasonMap[v.Season]++
	}
	seasons := make([]int, 0, len(seasonMap))
	for s := range seasonMap {
		seasons = append(seasons, s)
	}
	sort.Ints(seasons)

	items := make([]list.Item, len(seasons))
	for i, s := range seasons {
		watched := m.history != nil && m.history.IsSeasonWatched(imdbID, s, seasonMap[s])
		items[i] = seasonItem{season: s, episodeCount: seasonMap[s], watched: watched}
	}
	m.list.Title = "Seasons"
	m.list.SetItems(items)
}

func (m *DetailModel) showEpisodesForSeason(season int) {
	imdbID := m.historyID()
	var videos []stremio.Video
	for _, v := range m.meta.Videos {
		if v.Season == season {
			videos = append(videos, v)
		}
	}
	sort.Slice(videos, func(i, j int) bool { return videos[i].Episode < videos[j].Episode })
	items := make([]list.Item, len(videos))
	for i, v := range videos {
		watched := m.history != nil && m.history.IsEpisodeWatched(imdbID, v.Season, v.Episode)
		items[i] = videoItem{video: v, watched: watched}
	}
	m.list.Title = fmt.Sprintf("Season %d Episodes", season)
	m.list.SetItems(items)
}

func (m DetailModel) LoadMeta(nav NavigateToDetailMsg) tea.Cmd {
	// Snapshot the addon list to avoid racing with config mutations.
	configAddons := append([]string(nil), m.config.Addons...)
	return func() tea.Msg {
		ctx := context.Background()

		// Try the source addon first, then all others
		tried := make(map[string]bool)
		var addons []string
		if nav.BaseURL != "" {
			addons = append(addons, nav.BaseURL)
		}
		for _, u := range configAddons {
			if u != nav.BaseURL {
				addons = append(addons, u)
			}
		}

		var lastErr error
		for _, addonURL := range addons {
			if tried[addonURL] {
				continue
			}
			tried[addonURL] = true
			resp, err := m.client.FetchMeta(ctx, addonURL, nav.Type, nav.ID)
			if err != nil {
				lastErr = err
				continue
			}
			if resp.Meta.ID == "" && resp.Meta.Name == "" {
				lastErr = fmt.Errorf("empty meta response")
				continue
			}
			// Skip addon error responses (e.g. AIOStreams error meta)
			if strings.HasPrefix(resp.Meta.ID, "aiostreamserror") {
				lastErr = errors.New(resp.Meta.Description)
				continue
			}
			return metaLoadedMsg{meta: &resp.Meta}
		}

		if lastErr != nil {
			return metaErrorMsg{err: fmt.Errorf("could not load metadata: %w", lastErr)}
		}
		return metaErrorMsg{err: fmt.Errorf("no addon supports meta for this item")}
	}
}

func (m DetailModel) Update(msg tea.Msg) (DetailModel, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if m.loading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case metaLoadedMsg:
		m.loading = false
		m.meta = msg.meta
		m.contentType = msg.meta.Type
		m.viewingEpisodes = false
		m.selectedSeason = 0

		// If the meta provides an explicit IMDB ID and our requestedID uses
		// a different scheme (e.g. tvdb:), update to keep history consistent.
		if msg.meta.IMDBID != "" && m.requestedID != msg.meta.IMDBID {
			m.requestedID = msg.meta.IMDBID
		}

		if msg.meta.Type == "series" && len(msg.meta.Videos) > 0 {
			m.showSeasons()
		} else if len(msg.meta.Videos) > 0 {
			items := make([]list.Item, len(msg.meta.Videos))
			for i, v := range msg.meta.Videos {
				items[i] = videoItem{video: v}
			}
			m.list.SetItems(items)
		}
		return m, nil

	case metaErrorMsg:
		m.loading = false
		m.err = msg.err
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "escape":
			if m.viewingEpisodes {
				m.viewingEpisodes = false
				m.showSeasons()
				return m, nil
			}
		case "enter":
			if m.meta == nil {
				return m, nil
			}
			// For movies (no videos or single video), go to streams directly
			if m.meta.Type == "movie" || len(m.meta.Videos) == 0 {
				metaName := m.meta.Name
				metaYear := m.meta.ReleaseInfo
				return m, func() tea.Msg {
					return NavigateToStreamsMsg{
						ID:       m.meta.ID,
						Type:     m.meta.Type,
						MetaName: metaName,
						MetaYear: metaYear,
					}
				}
			}
			// If viewing season list, drill into episodes
			if !m.viewingEpisodes {
				if item, ok := m.list.SelectedItem().(seasonItem); ok {
					m.selectedSeason = item.season
					m.viewingEpisodes = true
					m.showEpisodesForSeason(item.season)
					return m, nil
				}
			}
			// If viewing episodes, navigate to streams
			if item, ok := m.list.SelectedItem().(videoItem); ok {
				metaName := m.meta.Name
				metaYear := m.meta.ReleaseInfo
				return m, func() tea.Msg {
					return NavigateToStreamsMsg{
						ID:       item.video.ID,
						Type:     m.meta.Type,
						MetaName: metaName,
						MetaYear: metaYear,
					}
				}
			}
		case "f":
			if m.meta != nil && m.meta.Type == "series" && len(m.meta.Videos) > 0 {
				var videos []stremio.Video
				if m.viewingEpisodes {
					for _, v := range m.meta.Videos {
						if v.Season == m.selectedSeason {
							videos = append(videos, v)
						}
					}
				} else {
					videos = m.meta.Videos
				}
				metaType := m.meta.Type
				metaName := m.meta.Name
				metaYear := m.meta.ReleaseInfo
				return m, func() tea.Msg {
					return NavigateToAllStreamsMsg{
						Videos:   videos,
						Type:     metaType,
						MetaName: metaName,
						MetaYear: metaYear,
					}
				}
			}
		case "w":
			if m.meta == nil || m.history == nil {
				return m, nil
			}
			imdbID := m.historyID()
			if m.viewingEpisodes {
				if item, ok := m.list.SelectedItem().(videoItem); ok {
					m.history.ToggleEpisode(imdbID, item.video.Season, item.video.Episode)
					if err := m.history.Save(); err != nil {
						m.saveErr = err
					} else {
						m.saveErr = nil
					}
					m.showEpisodesForSeason(m.selectedSeason)
				}
			} else if m.meta.Type == "series" && !m.viewingEpisodes {
				if item, ok := m.list.SelectedItem().(seasonItem); ok {
					var episodeNumbers []int
					for _, v := range m.meta.Videos {
						if v.Season == item.season {
							episodeNumbers = append(episodeNumbers, v.Episode)
						}
					}
					m.history.ToggleSeason(imdbID, item.season, episodeNumbers)
					if err := m.history.Save(); err != nil {
						m.saveErr = err
					} else {
						m.saveErr = nil
					}
					m.showSeasons()
				}
			} else if m.meta.Type == "movie" {
				m.history.ToggleMovie(imdbID)
				if err := m.history.Save(); err != nil {
					m.saveErr = err
				} else {
					m.saveErr = nil
				}
			}
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m DetailModel) View() string {
	if m.loading {
		return m.spinner.View() + " Loading details..."
	}
	if m.err != nil {
		return ErrorStyle.Render(fmt.Sprintf("Error: %v", m.err)) + "\n" + HelpStyle.Render("esc: back • tab: switch tab")
	}
	if m.meta == nil {
		return ""
	}

	var sections []string

	// Title
	sections = append(sections, TitleStyle.Render(m.meta.Name))

	// Info line
	var info []string
	if m.meta.ReleaseInfo != "" {
		info = append(info, m.meta.ReleaseInfo)
	}
	if m.meta.Runtime != "" {
		info = append(info, m.meta.Runtime)
	}
	if m.meta.IMDBRating != "" {
		info = append(info, "⭐ "+string(m.meta.IMDBRating))
	}
	if len(info) > 0 {
		sections = append(sections, SubtitleStyle.Render(strings.Join(info, " • ")))
	}

	// Genres
	if len(m.meta.Genres) > 0 {
		sections = append(sections, DetailLabelStyle.Render("Genres: ")+DetailValueStyle.Render(strings.Join(m.meta.Genres, ", ")))
	}

	// Cast
	if len(m.meta.Cast) > 0 {
		cast := m.meta.Cast
		if len(cast) > 5 {
			cast = cast[:5]
		}
		sections = append(sections, DetailLabelStyle.Render("Cast: ")+DetailValueStyle.Render(strings.Join(cast, ", ")))
	}

	// Description
	if m.meta.Description != "" {
		desc := m.meta.Description
		if utf8.RuneCountInString(desc) > 300 {
			runes := []rune(desc)
			desc = string(runes[:300]) + "..."
		}
		sections = append(sections, "")
		sections = append(sections, DetailValueStyle.Render(desc))
	}

	sections = append(sections, "")

	// Persistent save-error banner
	if m.saveErr != nil {
		sections = append(sections, ErrorStyle.Render(fmt.Sprintf("⚠ Could not save history: %v", m.saveErr)))
	}

	// For movies, show prompt to play
	if m.meta.Type == "movie" || len(m.meta.Videos) == 0 {
		sections = append(sections, HelpStyle.Render("enter: find streams • w: toggle watched • esc: back • q: quit"))
	} else if !m.viewingEpisodes {
		sections = append(sections, m.list.View())
		sections = append(sections, HelpStyle.Render("enter: view episodes • w: toggle watched • f: filter all episodes • esc: back • q: quit"))
	} else {
		sections = append(sections, m.list.View())
		sections = append(sections, HelpStyle.Render("enter: streams • w: toggle watched • f: filter season • esc: back to seasons • q: quit"))
	}

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}
