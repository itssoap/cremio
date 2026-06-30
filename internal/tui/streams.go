package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/itssoap/cremio/internal/config"
	"github.com/itssoap/cremio/internal/history"
	"github.com/itssoap/cremio/internal/player"
	"github.com/itssoap/cremio/internal/stremio"
)

type streamItem struct {
	stream       stremio.Stream
	episodeLabel string
	videoID      string
}

func (s streamItem) Title() string {
	name := s.stream.DisplayName()
	if tag := s.typeTag(); tag != "" {
		name = tag + " " + name
	}
	if s.episodeLabel != "" {
		return s.episodeLabel + " | " + name
	}
	return name
}

// typeTag returns a short prefix flagging streams that plain mpv cannot play
// on its own (e.g. torrents), so the user isn't surprised by a no-op launch.
func (s streamItem) typeTag() string {
	if s.stream.URL == "" && s.stream.InfoHash != "" {
		return "[torrent]"
	}
	return ""
}
func (s streamItem) Description() string {
	if s.stream.Description != "" {
		return s.stream.Description
	}
	if s.stream.Title != "" && s.stream.Title != s.stream.Name {
		return s.stream.Title
	}
	url := s.stream.PlayableURL()
	if utf8.RuneCountInString(url) > 60 {
		return string([]rune(url)[:60]) + "..."
	}
	return url
}
func (s streamItem) FilterValue() string { return s.stream.DisplayName() }

type StreamsModel struct {
	list          list.Model
	spinner       spinner.Model
	filterInput   textinput.Model
	client        *stremio.Client
	config        *config.Config
	allItems      []streamItem
	pendingVideos []stremio.Video
	pendingType   string
	contentID     string
	contentType   string
	metaName      string
	metaYear      string
	filterActive  bool
	infoMode      bool
	loading       bool
	launching     bool
	launched      bool
	launchSeq     int
	err           error
	playErr       error
	width         int
	height        int

	// Download state
	downloading    bool
	downloadMsg    string
	downloadResults []player.DownloadResult

	// Release group selector (batch download)
	rgSelectorActive  bool
	rgItems           []string           // available release groups
	rgCursor          int
	rgEpisodes        []episodeSelection // episodes for batch download
	rgEpSelectorActive bool
	rgEpCursor         int
}

type streamsLoadedMsg struct {
	streams []stremio.Stream
}
type streamsErrorMsg struct {
	err error
}
type mpvLaunchedMsg struct {
	videoID   string
	videoType string
}
type mpvErrorMsg struct {
	err error
}
type clearLaunchedMsg struct{ seq int }

type episodeSelection struct {
	label    string // e.g. "S01E05"
	season   int
	episode  int
	epTitle  string
	selected bool
}

func (m *StreamsModel) resetDownloadState() {
	m.downloading = false
	m.downloadMsg = ""
	m.downloadResults = nil
	m.rgSelectorActive = false
	m.rgItems = nil
	m.rgCursor = 0
	m.rgEpisodes = nil
	m.rgEpSelectorActive = false
	m.rgEpCursor = 0
}

func NewStreamsModel(client *stremio.Client, cfg *config.Config) StreamsModel {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Streams"
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("170"))

	fi := textinput.New()
	fi.Placeholder = "Filter: +include -exclude ..."
	fi.CharLimit = 200

	return StreamsModel{
		list:        l,
		spinner:     s,
		filterInput: fi,
		client:      client,
		config:      cfg,
	}
}

func (m *StreamsModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.filterInput.Width = w - 4
	m.updateListSize()
}

func (m *StreamsModel) updateListSize() {
	listH := m.height - 7 // filter input (2) + help (1) + spacing
	if m.infoMode {
		listH -= 6 // info panel: 4 content lines + 2 border rows
	}
	if listH < 3 {
		listH = 3
	}
	m.list.SetSize(m.width, listH)
}

func (m StreamsModel) LoadStreams(nav NavigateToStreamsMsg) tea.Cmd {
	// Snapshot the addon list to avoid racing with config mutations.
	addons := append([]string(nil), m.config.Addons...)
	return func() tea.Msg {
		var allStreams []stremio.Stream
		ctx := context.Background()

		for _, addonURL := range addons {
			resp, err := m.client.FetchStreams(ctx, addonURL, nav.Type, nav.ID)
			if err != nil {
				continue
			}
			allStreams = append(allStreams, resp.Streams...)
		}

		if len(allStreams) == 0 {
			return streamsErrorMsg{err: fmt.Errorf("no streams found")}
		}
		return streamsLoadedMsg{streams: allStreams}
	}
}

func (m StreamsModel) LoadAllStreams(nav NavigateToAllStreamsMsg, filter Filter) tea.Cmd {
	// Snapshot the addon list to avoid racing with config mutations.
	addons := append([]string(nil), m.config.Addons...)
	return func() tea.Msg {
		var allStreams []labeledStream
		ctx := context.Background()

		for _, video := range nav.Videos {
			label := fmt.Sprintf("S%02dE%02d", video.Season, video.Episode)
			for _, addonURL := range addons {
				resp, err := m.client.FetchStreams(ctx, addonURL, nav.Type, video.ID)
				if err != nil {
					continue
				}
				for _, s := range resp.Streams {
					if filter.IsEmpty() || filter.Match(s.Name, s.Title) {
						allStreams = append(allStreams, labeledStream{stream: s, label: label, videoID: video.ID})
					}
				}
			}
		}

		if len(allStreams) == 0 {
			return streamsErrorMsg{err: fmt.Errorf("no streams found matching filter")}
		}
		return allStreamsLoadedMsg{streams: allStreams}
	}
}

type labeledStream struct {
	stream  stremio.Stream
	label   string
	videoID string
}

type allStreamsLoadedMsg struct {
	streams []labeledStream
}

func (m *StreamsModel) applyFilter() {
	f := ParseFilter(m.filterInput.Value())
	var filtered []list.Item
	for _, item := range m.allItems {
		if f.IsEmpty() || f.Match(item.stream.Name, item.stream.Title) {
			filtered = append(filtered, item)
		}
	}
	m.list.SetItems(filtered)
}

// buildPlaylist collects the first playable stream per episode label from the
// current visible list, preserving episode order. Returns the URL list and the
// 0-based index of the selected item's episode.
func (m *StreamsModel) buildPlaylist(selected streamItem) (urls []string, startIdx int) {
	seen := make(map[string]bool)
	var ordered []struct {
		label string
		url   string
	}

	for _, li := range m.list.Items() {
		si, ok := li.(streamItem)
		if !ok || si.episodeLabel == "" {
			continue
		}
		url := si.stream.PlayableURL()
		if url == "" || seen[si.episodeLabel] {
			continue
		}
		seen[si.episodeLabel] = true
		ordered = append(ordered, struct {
			label string
			url   string
		}{si.episodeLabel, url})
	}

	urls = make([]string, len(ordered))
	for i, entry := range ordered {
		urls[i] = entry.url
		if entry.label == selected.episodeLabel {
			startIdx = i
		}
	}
	return urls, startIdx
}

// isBatchMode returns true if the streams view is showing multi-episode results.
func (m *StreamsModel) isBatchMode() bool {
	for _, item := range m.allItems {
		if item.episodeLabel != "" {
			return true
		}
	}
	return false
}

// collectReleaseGroups extracts unique release groups from visible stream items.
func (m *StreamsModel) collectReleaseGroups() []string {
	seen := make(map[string]bool)
	var groups []string
	for _, li := range m.list.Items() {
		si, ok := li.(streamItem)
		if !ok {
			continue
		}
		group := player.ExtractReleaseGroup(si.stream.Name)
		if !seen[group] {
			seen[group] = true
			groups = append(groups, group)
		}
	}
	return groups
}

// collectEpisodesForSelection builds the episode selector from pending videos.
func (m *StreamsModel) collectEpisodesForSelection() []episodeSelection {
	var eps []episodeSelection
	for _, v := range m.pendingVideos {
		eps = append(eps, episodeSelection{
			label:    fmt.Sprintf("S%02dE%02d", v.Season, v.Episode),
			season:   v.Season,
			episode:  v.Episode,
			epTitle:  v.DisplayTitle(),
			selected: true,
		})
	}
	return eps
}

// streamsForGroupAndEpisode finds the best HTTP stream for a release group and episode.
func (m *StreamsModel) streamForGroupAndEpisode(group, label string) *streamItem {
	for _, li := range m.list.Items() {
		si, ok := li.(streamItem)
		if !ok || si.episodeLabel != label {
			continue
		}
		g := player.ExtractReleaseGroup(si.stream.Name)
		if g != group {
			continue
		}
		url := si.stream.PlayableURL()
		if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
			return &si
		}
	}
	return nil
}

// downloadSingle downloads a single stream (movie or single episode).
func (m *StreamsModel) downloadSingle(item streamItem) tea.Cmd {
	url := item.stream.PlayableURL()
	metaName := m.metaName
	metaYear := m.metaYear
	contentType := m.contentType
	videoID := item.videoID
	if videoID == "" {
		videoID = m.contentID
	}

	return func() tea.Msg {
		downloadDir := "."
		ext := player.GuessExtension(url)

		var destPath string
		if contentType == "movie" {
			destPath = player.PlexMoviePath(downloadDir, metaName, metaYear, ext)
		} else {
			season, episode := history.ParseEpisodeID(videoID)
			destPath = player.PlexEpisodePath(downloadDir, metaName, metaYear, season, episode, "", ext)
		}

		start := time.Now()
		result, err := player.DownloadHTTP(context.Background(), url, destPath)
		dr := player.DownloadResult{
			Label:    metaName,
			Path:     destPath,
			Duration: time.Since(start),
		}
		if err != nil {
			dr.Err = err
		} else if result.Skipped {
			dr.Skipped = true
			dr.SkipMsg = result.SkipReason
		}
		return dr
	}
}

// downloadBatchByGroup downloads all selected episodes from a release group.
func (m *StreamsModel) downloadBatchByGroup(group string) tea.Cmd {
	metaName := m.metaName
	metaYear := m.metaYear

	type dlJob struct {
		url     string
		path    string
		label   string
		season  int
		episode int
	}

	var jobs []dlJob
	var skipped []player.DownloadResult

	for _, ep := range m.rgEpisodes {
		if !ep.selected {
			continue
		}
		si := m.streamForGroupAndEpisode(group, ep.label)
		if si == nil {
			skipped = append(skipped, player.DownloadResult{
				Label:   ep.label,
				Skipped: true,
				SkipMsg: fmt.Sprintf("not available from %s", group),
			})
			continue
		}
		url := si.stream.PlayableURL()
		ext := player.GuessExtension(url)
		destPath := player.PlexEpisodePath(".", metaName, metaYear, ep.season, ep.episode, ep.epTitle, ext)
		jobs = append(jobs, dlJob{url: url, path: destPath, label: ep.label, season: ep.season, episode: ep.episode})
	}

	return func() tea.Msg {
		var results []player.DownloadResult
		results = append(results, skipped...)

		for _, job := range jobs {
			start := time.Now()
			result, err := player.DownloadHTTP(context.Background(), job.url, job.path)
			dr := player.DownloadResult{
				Label:    job.label,
				Path:     job.path,
				Duration: time.Since(start),
			}
			if err != nil {
				dr.Err = err
			} else if result.Skipped {
				dr.Skipped = true
				dr.SkipMsg = result.SkipReason
			}
			results = append(results, dr)
		}
		return player.BatchDownloadResult{Results: results}
	}
}

func (m StreamsModel) Update(msg tea.Msg) (StreamsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if m.loading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case streamsLoadedMsg:
		m.loading = false
		m.allItems = make([]streamItem, len(msg.streams))
		for i, s := range msg.streams {
			m.allItems[i] = streamItem{stream: s}
		}
		m.applyFilter()
		return m, nil

	case allStreamsLoadedMsg:
		m.loading = false
		m.allItems = make([]streamItem, len(msg.streams))
		for i, ls := range msg.streams {
			m.allItems[i] = streamItem{stream: ls.stream, episodeLabel: ls.label, videoID: ls.videoID}
		}
		m.applyFilter()
		return m, nil

	case streamsErrorMsg:
		m.loading = false
		m.err = msg.err
		return m, nil

	case mpvLaunchedMsg:
		m.launching = false
		m.launched = true
		m.launchSeq++
		seq := m.launchSeq
		return m, tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
			return clearLaunchedMsg{seq: seq}
		})

	case mpvErrorMsg:
		m.launching = false
		m.launched = false
		m.playErr = msg.err
		return m, nil

	case clearLaunchedMsg:
		if msg.seq == m.launchSeq {
			m.launched = false
		}
		return m, nil

	case player.DownloadResult:
		m.downloading = false
		if msg.Err != nil {
			m.downloadMsg = fmt.Sprintf("✗ Download failed: %v", msg.Err)
		} else if msg.Skipped {
			m.downloadMsg = fmt.Sprintf("⊘ Skipped: %s", msg.SkipMsg)
		} else {
			m.downloadMsg = fmt.Sprintf("✓ Downloaded to %s (%s)", msg.Path, msg.Duration.Round(time.Second))
		}
		return m, nil

	case player.BatchDownloadResult:
		m.downloading = false
		m.downloadResults = msg.Results
		var ok, skip, fail int
		for _, r := range msg.Results {
			switch {
			case r.Err != nil:
				fail++
			case r.Skipped:
				skip++
			default:
				ok++
			}
		}
		m.downloadMsg = fmt.Sprintf("Batch done: %d downloaded, %d skipped, %d failed", ok, skip, fail)
		return m, nil

	case tea.KeyMsg:
		// Episode selector popup
		if m.rgEpSelectorActive {
			switch msg.String() {
			case "up", "k":
				if m.rgEpCursor > 0 {
					m.rgEpCursor--
				}
			case "down", "j":
				if m.rgEpCursor < len(m.rgEpisodes)-1 {
					m.rgEpCursor++
				}
			case " ":
				m.rgEpisodes[m.rgEpCursor].selected = !m.rgEpisodes[m.rgEpCursor].selected
			case "enter":
				// Move to release group selector
				m.rgEpSelectorActive = false
				m.rgItems = m.collectReleaseGroups()
				m.rgCursor = 0
				m.rgSelectorActive = true
			case "esc":
				m.rgEpSelectorActive = false
				m.rgEpisodes = nil
			}
			return m, nil
		}

		// Release group selector popup
		if m.rgSelectorActive {
			switch msg.String() {
			case "up", "k":
				if m.rgCursor > 0 {
					m.rgCursor--
				}
			case "down", "j":
				if m.rgCursor < len(m.rgItems)-1 {
					m.rgCursor++
				}
			case "enter":
				group := m.rgItems[m.rgCursor]
				m.rgSelectorActive = false
				m.downloading = true
				m.downloadMsg = fmt.Sprintf("Downloading from %s...", group)
				return m, m.downloadBatchByGroup(group)
			case "esc":
				m.rgSelectorActive = false
				m.rgItems = nil
			}
			return m, nil
		}

		if m.filterActive {
			switch msg.String() {
			case "enter":
				m.filterActive = false
				m.filterInput.Blur()
				// If we have pending videos (batch series mode), fetch now with filter
				if len(m.pendingVideos) > 0 {
					f := ParseFilter(m.filterInput.Value())
					if f.IsEmpty() {
						return m, nil
					}
					m.loading = true
					m.err = nil
					nav := NavigateToAllStreamsMsg{Videos: m.pendingVideos, Type: m.pendingType}
					return m, tea.Batch(m.spinner.Tick, m.LoadAllStreams(nav, f))
				}
				m.applyFilter()
				return m, nil
			case "esc":
				m.filterActive = false
				m.filterInput.Blur()
				return m, nil
			}
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "/":
			m.filterActive = true
			return m, m.filterInput.Focus()
		case "i":
			m.infoMode = !m.infoMode
			m.updateListSize()
			return m, nil
		case "c":
			m.filterInput.SetValue("")
			m.applyFilter()
			return m, nil
		case "d":
			if m.downloading {
				return m, nil
			}
			if m.isBatchMode() {
				// Batch mode: show episode selector popup
				if len(m.pendingVideos) == 0 {
					m.downloadMsg = "Download not available — apply a filter first (f from season view)"
					return m, nil
				}
				m.rgEpisodes = m.collectEpisodesForSelection()
				m.rgEpCursor = 0
				m.rgEpSelectorActive = true
				return m, nil
			}
			// Single stream: download directly
			if item, ok := m.list.SelectedItem().(streamItem); ok {
				url := item.stream.PlayableURL()
				if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
					m.downloadMsg = "Only HTTP/HTTPS streams can be downloaded"
					return m, nil
				}
				m.downloading = true
				m.downloadMsg = "Downloading..."
				return m, m.downloadSingle(item)
			}
		case "enter":
			if m.launching {
				return m, nil
			}
			if item, ok := m.list.SelectedItem().(streamItem); ok {
				m.launching = true
				m.playErr = nil
				videoID := item.videoID
				contentType := m.contentType
				if videoID == "" {
					videoID = m.contentID
				}

				// Batch mode: build playlist (one stream per episode)
				if item.episodeLabel != "" && m.config.PlaylistMode {
					playlist, startIdx := m.buildPlaylist(item)
					if len(playlist) > 1 {
						return m, func() tea.Msg {
							err := player.PlayWithMPVPlaylist(playlist, startIdx)
							if err != nil {
								return mpvErrorMsg{err: err}
							}
							return mpvLaunchedMsg{videoID: videoID, videoType: contentType}
						}
					}
				}

				// Single stream fallback
				url := item.stream.PlayableURL()
				return m, func() tea.Msg {
					err := player.PlayWithMPV(url)
					if err != nil {
						return mpvErrorMsg{err: err}
					}
					return mpvLaunchedMsg{videoID: videoID, videoType: contentType}
				}
			}
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m StreamsModel) infoPanel() string {
	item, ok := m.list.SelectedItem().(streamItem)
	if !ok {
		return InfoPanelStyle.Width(m.width - 4).Render(
			SubtitleStyle.Render("No stream selected"),
		)
	}
	s := item.stream

	var rows []string

	// Addon name — first line of stream.Name
	if s.Name != "" {
		addonName := strings.SplitN(s.Name, "\n", 2)[0]
		rows = append(rows, DetailLabelStyle.Render("Addon:   ")+DetailValueStyle.Render(strings.TrimSpace(addonName)))
	}

	// Details (size / seeders / language) — stream.Title
	if s.Title != "" {
		rows = append(rows, DetailLabelStyle.Render("Details: ")+DetailValueStyle.Render(strings.TrimSpace(s.Title)))
	}

	// Extra description
	if s.Description != "" {
		rows = append(rows, DetailLabelStyle.Render("Info:    ")+DetailValueStyle.Render(strings.TrimSpace(s.Description)))
	}

	// Stream type
	streamType := "Unknown"
	switch {
	case s.URL != "":
		streamType = "HTTP"
	case s.InfoHash != "":
		streamType = "Torrent"
		if s.FileIdx != nil {
			streamType += fmt.Sprintf(" (file #%d)", *s.FileIdx)
		}
	case s.YtID != "":
		streamType = "YouTube"
	case s.ExternalURL != "":
		streamType = "External"
	}
	rows = append(rows, DetailLabelStyle.Render("Type:    ")+DetailValueStyle.Render(streamType))

	return InfoPanelStyle.Width(m.width - 4).Render(strings.Join(rows, "\n"))
}

func (m StreamsModel) View() string {
	if m.loading {
		return m.spinner.View() + " Loading streams..."
	}
	if m.err != nil {
		return ErrorStyle.Render(fmt.Sprintf("Error: %v", m.err))
	}

	// Episode selector popup
	if m.rgEpSelectorActive {
		return m.episodeSelectorView()
	}

	// Release group selector popup
	if m.rgSelectorActive {
		return m.releaseGroupSelectorView()
	}

	var sections []string
	sections = append(sections, m.filterInput.View())

	// If waiting for filter input in batch mode, show hint
	if len(m.pendingVideos) > 0 && len(m.allItems) == 0 {
		sections = append(sections, HelpStyle.Render("Type a filter and press enter to search all episodes"))
		sections = append(sections, HelpStyle.Render("/ filter • i: info • esc: back • q: quit"))
		return lipgloss.JoinVertical(lipgloss.Left, sections...)
	}

	view := m.list.View()
	if m.launching {
		view += "\n" + SubtitleStyle.Render("▶ Launching mpv...")
	} else if m.launched {
		view += "\n" + SubtitleStyle.Render("▶ Launched")
	}
	if m.downloading {
		view += "\n" + SubtitleStyle.Render("⬇ " + m.downloadMsg)
	} else if m.downloadMsg != "" {
		view += "\n" + SubtitleStyle.Render(m.downloadMsg)
	}
	if m.playErr != nil {
		view += "\n" + ErrorStyle.Render(fmt.Sprintf("MPV error: %v", m.playErr))
	}
	// Show batch download details
	if len(m.downloadResults) > 0 {
		for _, r := range m.downloadResults {
			switch {
			case r.Err != nil:
				view += "\n" + ErrorStyle.Render(fmt.Sprintf("  ✗ %s: %v", r.Label, r.Err))
			case r.Skipped:
				view += "\n" + HelpStyle.Render(fmt.Sprintf("  ⊘ %s: %s", r.Label, r.SkipMsg))
			default:
				view += "\n" + SubtitleStyle.Render(fmt.Sprintf("  ✓ %s → %s", r.Label, r.Path))
			}
		}
	}
	sections = append(sections, view)
	if m.infoMode {
		sections = append(sections, m.infoPanel())
	}
	sections = append(sections, HelpStyle.Render("/ filter • c clear • d: download • i: info • enter: play • esc: back • q: quit"))
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m StreamsModel) episodeSelectorView() string {
	var b strings.Builder
	b.WriteString(TitleStyle.Render("Select episodes to download"))
	b.WriteString("\n\n")

	for i, ep := range m.rgEpisodes {
		cursor := "  "
		if i == m.rgEpCursor {
			cursor = "▶ "
		}
		check := "☐"
		if ep.selected {
			check = "☑"
		}
		line := fmt.Sprintf("%s%s %s - %s", cursor, check, ep.label, ep.epTitle)
		if i == m.rgEpCursor {
			b.WriteString(SubtitleStyle.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(HelpStyle.Render("space: toggle • enter: confirm • esc: cancel"))
	return b.String()
}

func (m StreamsModel) releaseGroupSelectorView() string {
	var b strings.Builder
	b.WriteString(TitleStyle.Render("Select release group"))
	b.WriteString("\n\n")

	for i, group := range m.rgItems {
		cursor := "  "
		if i == m.rgCursor {
			cursor = "▶ "
		}
		// Count how many selected episodes have streams from this group
		avail := 0
		for _, ep := range m.rgEpisodes {
			if ep.selected && m.streamForGroupAndEpisode(group, ep.label) != nil {
				avail++
			}
		}
		selected := 0
		for _, ep := range m.rgEpisodes {
			if ep.selected {
				selected++
			}
		}
		line := fmt.Sprintf("%s%s (%d/%d episodes available)", cursor, group, avail, selected)
		if i == m.rgCursor {
			b.WriteString(SubtitleStyle.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(HelpStyle.Render("enter: download from group • esc: cancel"))
	return b.String()
}
