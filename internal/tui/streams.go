package tui

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
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

// fileName resolves the real download filename for this stream, preferring the
// addon-provided behaviorHints.filename, then a filename in the URL, then a
// filename-looking Title/Description. Returns "" if none can be determined.
func (s streamItem) fileName() string {
	if f := s.stream.Filename(); f != "" {
		return strings.TrimSpace(f)
	}
	if f := player.FilenameFromURL(s.stream.PlayableURL()); f != "" {
		return f
	}
	for _, cand := range []string{s.stream.Title, s.stream.Description} {
		cand = strings.TrimSpace(cand)
		// Strip a leading folder emoji some addons prepend.
		cand = strings.TrimSpace(strings.TrimPrefix(cand, "\U0001f4c1"))
		if looksLikeFilename(cand) {
			return cand
		}
	}
	return ""
}

func looksLikeFilename(s string) bool {
	if strings.ContainsAny(s, "\n") {
		return false
	}
	switch strings.ToLower(fileExt(s)) {
	case ".mkv", ".mp4", ".avi", ".webm", ".ts":
		return true
	}
	return false
}

func fileExt(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 && i > len(s)-6 {
		return s[i:]
	}
	return ""
}

// streamTypeLabel returns a short label for the stream's delivery type.
func streamTypeLabel(s stremio.Stream) string {
	switch {
	case s.URL != "":
		return "HTTP"
	case s.InfoHash != "":
		if s.FileIdx != nil {
			return fmt.Sprintf("Torrent (file #%d)", *s.FileIdx)
		}
		return "Torrent"
	case s.YtID != "":
		return "YouTube"
	case s.ExternalURL != "":
		return "External"
	}
	return "Unknown"
}

// providerTag returns the addon/debrid source tag the addon conveys, e.g.
// "Strem Torz | RD", "Comet | TB", or "Torrentio". Aggregators such as
// AIOStreams embed it in the description as a line like "Addon : Strem Torz | RD";
// others (Torrentio-style) use the first line of Name. Returns "" when the
// stream carries no recognisable provider tag (Name is release info only).
func providerTag(s stremio.Stream) string {
	for _, line := range strings.Split(s.Description, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "addon") {
			if i := strings.IndexByte(line, ':'); i >= 0 {
				if v := strings.TrimSpace(line[i+1:]); v != "" {
					return v
				}
			}
		}
	}
	// Torrentio-style addons put their brand on the first line of a multi-line Name.
	if name := strings.TrimSpace(s.Name); strings.Contains(name, "\n") {
		return strings.TrimSpace(name[:strings.IndexByte(name, '\n')])
	}
	// Single-line names like "GDrive 2160p": the first token is the addon brand
	// unless it is pure release info (e.g. "1080p - WEB-DL - [FLE]").
	if fields := strings.Fields(s.Name); len(fields) > 0 && !isQualityToken(fields[0]) {
		return fields[0]
	}
	return ""
}

// isQualityToken reports whether a token is resolution/quality info rather than
// an addon or provider name.
func isQualityToken(t string) bool {
	switch strings.ToLower(t) {
	case "4k", "2160p", "1080p", "720p", "480p", "uhd", "fhd", "hd", "sd":
		return true
	}
	return false
}

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
	downloadMode  bool // when true, auto-show release group selector after streams load
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
	downloadMsg string

	// Release group selector (batch download: groups first)
	rgSelectorActive bool
	rgGroups         []releaseGroupInfo
	rgCursor         int
	rgScroll         int

	// Episode selector (after group chosen)
	rgEpSelectorActive bool
	rgEpisodes         []episodeSelection
	rgEpCursor         int
	rgEpScroll         int
	rgSelectedGroup    string
}

type streamsLoadedMsg struct {
	streams []loadedStream
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

// Messages for download flow (handled by app.go which owns the manager).
type enqueueDownloadsMsg struct{}
type enqueueSingleDownloadMsg struct{ item streamItem }

type episodeSelection struct {
	label     string // e.g. "S01E05"
	season    int
	episode   int
	filename  string // actual file name (distinguishes v1 / v2 cuts)
	url       string // resolved playable URL for this exact file
	size      string // parsed size display
	sizeBytes int64
	selected  bool
}

type releaseGroupInfo struct {
	name       string
	resolution string
	epCount    int
	totalSize  int64
}

func (m *StreamsModel) resetDownloadState() {
	m.downloadMsg = ""
	m.rgSelectorActive = false
	m.rgGroups = nil
	m.rgCursor = 0
	m.rgScroll = 0
	m.rgEpisodes = nil
	m.rgEpSelectorActive = false
	m.rgEpCursor = 0
	m.rgEpScroll = 0
	m.rgSelectedGroup = ""
}

func (m *StreamsModel) popupVisibleRows() int {
	rows := m.height - 8
	if rows < 5 {
		rows = 5
	}
	return rows
}

func NewStreamsModel(client *stremio.Client, cfg *config.Config) StreamsModel {
	l := newList()
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
		listH -= 9 // info panel: up to 7 content lines + 2 border rows
	}
	if listH < 3 {
		listH = 3
	}
	m.list.SetSize(m.width, listH)
}

func (m StreamsModel) LoadStreams(nav NavigateToStreamsMsg) tea.Cmd {
	// Snapshot the addon list to avoid racing with config mutations.
	addons := append([]string(nil), m.config.Addons...)
	client := m.client
	return func() tea.Msg {
		ctx := context.Background()

		type res struct{ streams []stremio.Stream }
		results := make([]res, len(addons))
		var wg sync.WaitGroup
		for i, addonURL := range addons {
			wg.Add(1)
			go func(i int, url string) {
				defer wg.Done()
				// Retry + drop error placeholders here too, so a rate-limited
				// single view recovers instead of showing "Rate Limit Exceeded".
				results[i] = res{streams: fetchStreamsWithRetry(ctx, client, url, nav.Type, nav.ID)}
			}(i, addonURL)
		}
		wg.Wait()

		var out []loadedStream
		for i := range addons {
			for _, s := range results[i].streams {
				out = append(out, loadedStream{stream: s})
			}
		}
		if len(out) == 0 {
			return streamsErrorMsg{err: fmt.Errorf("no streams found")}
		}
		return streamsLoadedMsg{streams: out}
	}
}

// fetchStreamsWithRetry fetches an addon's streams and returns only real content
// streams. Aggregators rate-limit and answer HTTP 200 with a single non-content
// "Rate Limit Exceeded" placeholder; that is indistinguishable from success by
// the error alone, so it is detected (a response that has streams but none are
// content) and retried with exponential backoff + jitter. A genuinely empty
// result (zero streams) is returned as-is. Placeholder streams are never
// returned, so a still-limited episode contributes nothing rather than a fake row.
func fetchStreamsWithRetry(ctx context.Context, client *stremio.Client, addonURL, contentType, id string) []stremio.Stream {
	const maxAttempts = 4
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err := client.FetchStreams(ctx, addonURL, contentType, id)
		if err == nil {
			var content []stremio.Stream
			for _, s := range resp.Streams {
				if s.IsContent() {
					content = append(content, s)
				}
			}
			// Real streams, or a genuine empty result -> done.
			if len(content) > 0 || len(resp.Streams) == 0 {
				return content
			}
			// Only non-content streams came back (error placeholder) -> retry.
		}
		if attempt < maxAttempts {
			backoff := time.Duration(300<<uint(attempt-1))*time.Millisecond + time.Duration(rand.Intn(200))*time.Millisecond
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil
			}
		}
	}
	return nil
}

func (m StreamsModel) LoadAllStreams(nav NavigateToAllStreamsMsg, filter Filter) tea.Cmd {
	// Snapshot the addon list to avoid racing with config mutations.
	addons := append([]string(nil), m.config.Addons...)
	client := m.client
	perAddon := m.config.StreamFetchConcurrencyOrDefault()
	return func() tea.Msg {
		ctx := context.Background()

		type key struct {
			videoIdx int
			addonIdx int
		}
		type res struct {
			key     key
			label   string
			videoID string
			streams []stremio.Stream
		}
		results := make([]res, len(nav.Videos)*len(addons))

		// Bound concurrency PER ADDON (not globally): aggregators return
		// truncated/placeholder results and drop whole episodes when hit with
		// many concurrent requests, so requests to one addon are serialized by
		// default (StreamFetchConcurrency, default 1) while different addons
		// still run in parallel. fetchStreamsWithRetry recovers stragglers.
		addonSem := make(map[string]chan struct{}, len(addons))
		for _, a := range addons {
			addonSem[a] = make(chan struct{}, perAddon)
		}

		var wg sync.WaitGroup
		for vi, video := range nav.Videos {
			label := fmt.Sprintf("S%02dE%02d", video.Season, video.Episode)
			for ai, addonURL := range addons {
				wg.Add(1)
				idx := vi*len(addons) + ai
				go func(idx int, k key, label, videoID, addonURL string) {
					defer wg.Done()
					sem := addonSem[addonURL]
					sem <- struct{}{}
					defer func() { <-sem }()
					results[idx] = res{
						key:     k,
						label:   label,
						videoID: videoID,
						streams: fetchStreamsWithRetry(ctx, client, addonURL, nav.Type, videoID),
					}
				}(idx, key{vi, ai}, label, video.ID, addonURL)
			}
		}
		wg.Wait()

		var allStreams []labeledStream
		for _, r := range results {
			for _, s := range r.streams {
				if filter.IsEmpty() || filter.Match(s.Name, s.Title, s.Description) {
					allStreams = append(allStreams, labeledStream{
						stream: s, label: r.label, videoID: r.videoID,
					})
				}
			}
		}

		if len(allStreams) == 0 {
			return streamsErrorMsg{err: fmt.Errorf("no streams found matching filter")}
		}
		return allStreamsLoadedMsg{streams: allStreams}
	}
}

type loadedStream struct {
	stream stremio.Stream
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
	gf := m.config.GlobalFilters
	var filtered []list.Item
	for _, item := range m.allItems {
		if !passesGlobalFilters(gf, item) {
			continue
		}
		if f.IsEmpty() || f.Match(item.stream.Name, item.stream.Title, item.stream.Description) {
			filtered = append(filtered, item)
		}
	}
	m.list.SetItems(filtered)
}

// passesGlobalFilters evaluates the persistent per-field global filters against
// a stream. Empty filter fields are ignored.
func passesGlobalFilters(gf config.GlobalFilters, item streamItem) bool {
	if gf.IsEmpty() {
		return true
	}
	s := item.stream
	if gf.Addon != "" && !ParseFilter(gf.Addon).Match(providerTag(s)) {
		return false
	}
	if gf.FileInfo != "" && !ParseFilter(gf.FileInfo).Match(s.Name, s.Title, s.Description, item.fileName()) {
		return false
	}
	if gf.FileSource != "" && !ParseFilter(gf.FileSource).Match(player.ParseSource(s.Name, s.Title, s.Description)) {
		return false
	}
	if gf.Type != "" && !ParseFilter(gf.Type).Match(streamTypeLabel(s)) {
		return false
	}
	if gf.ReleaseGroup != "" && !ParseFilter(gf.ReleaseGroup).Match(player.ReleaseGroup(s.Name, s.Filename())) {
		return false
	}
	return true
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

// releaseVariant returns a stable identity for a specific release, combining
// group, resolution, source and codec (e.g. "FLE - 1080p - BluRay - HEVC").
// Grouping downloads by this instead of the bare release group ensures every
// episode in a chosen group is the SAME encode, not an arbitrary mix of the
// group's BluRay / WEB-DL / codec variants.
func releaseVariant(s stremio.Stream) string {
	parts := []string{player.ReleaseGroup(s.Name, s.Filename())}
	if res := player.ParseResolution(s.Name, s.Title, s.Description); res != "" {
		parts = append(parts, res)
	}
	if src := player.ParseSource(s.Name, s.Title, s.Description); src != "" {
		parts = append(parts, src)
	}
	if codec := player.ParseCodec(s.Name, s.Title, s.Description); codec != "" {
		parts = append(parts, codec)
	}
	variant := strings.Join(parts, " - ")
	// A season pack shares its release group/res/codec with the per-episode
	// files of the same group, but is a distinct thing (one release covering the
	// whole season). Keep it in its own variant so it neither collapses into nor
	// double-downloads alongside the per-episode files.
	if player.IsSeasonPack(s.Filename()) {
		variant += " (Season Pack)"
	}
	return variant
}

// collectReleaseGroups extracts unique release groups with metadata from visible streams.
func (m *StreamsModel) collectReleaseGroups() []releaseGroupInfo {
	type groupData struct {
		resolution string
		episodes   map[string]bool
		totalSize  int64
		packSized  bool
	}
	groups := make(map[string]*groupData)
	var order []string

	for _, li := range m.list.Items() {
		si, ok := li.(streamItem)
		if !ok {
			continue
		}
		group := releaseVariant(si.stream)
		gd, exists := groups[group]
		if !exists {
			gd = &groupData{episodes: make(map[string]bool)}
			groups[group] = gd
			order = append(order, group)
		}
		if si.episodeLabel != "" && !gd.episodes[si.episodeLabel] {
			gd.episodes[si.episodeLabel] = true
			// A season pack reports the whole-season size on every episode entry;
			// count it once instead of multiplying it by the episode count.
			if player.IsSeasonPack(si.fileName()) {
				if !gd.packSized {
					sizeBytes, _ := player.ParseSize(si.stream.Name, si.stream.Title)
					gd.totalSize += sizeBytes
					gd.packSized = true
				}
			} else {
				sizeBytes, _ := player.ParseSize(si.stream.Name, si.stream.Title)
				gd.totalSize += sizeBytes
			}
		}
		if gd.resolution == "" {
			gd.resolution = player.ParseResolution(si.stream.Name, si.stream.Title)
		}
	}

	var result []releaseGroupInfo
	for _, name := range order {
		gd := groups[name]
		result = append(result, releaseGroupInfo{
			name:       name,
			resolution: gd.resolution,
			epCount:    len(gd.episodes),
			totalSize:  gd.totalSize,
		})
	}
	return result
}

// collectEpisodesForGroup builds the episode selector for a specific release
// variant. Distinct files are kept per episode, so alternate cuts (S01E01 and
// S01E01v2) both appear; only byte-identical duplicates (same file from another
// source) are collapsed. Rows are sorted by season, episode, then filename.
func (m *StreamsModel) collectEpisodesForGroup(group string) []episodeSelection {
	seen := make(map[string]bool)
	var eps []episodeSelection

	for _, li := range m.list.Items() {
		si, ok := li.(streamItem)
		if !ok || si.episodeLabel == "" {
			continue
		}
		if releaseVariant(si.stream) != group {
			continue
		}
		url := si.stream.PlayableURL()
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			continue
		}
		fname := si.fileName()
		// Key rows by episode identity + file so: alternate cuts of one episode
		// (same videoID, different file) both show; a genuine duplicate (same
		// episode, same file from another source) collapses; and a season pack
		// offered per episode (same file name, different videoID) keeps one row
		// per episode instead of collapsing to a single row.
		fileKey := fname
		if fileKey == "" {
			fileKey = url
		}
		dedupKey := si.videoID + "|" + fileKey
		if seen[dedupKey] {
			continue
		}
		seen[dedupKey] = true
		sizeBytes, sizeDisplay := player.ParseSize(si.stream.Name, si.stream.Title, si.stream.Description)
		if vs := si.stream.VideoSize(); vs > 0 {
			sizeBytes = vs
			sizeDisplay = player.FormatBytes(vs)
		}
		season, episode := history.ParseEpisodeID(si.videoID)
		eps = append(eps, episodeSelection{
			label:     si.episodeLabel,
			season:    season,
			episode:   episode,
			filename:  fname,
			url:       url,
			size:      sizeDisplay,
			sizeBytes: sizeBytes,
			selected:  true,
		})
	}

	sort.SliceStable(eps, func(i, j int) bool {
		if eps[i].season != eps[j].season {
			return eps[i].season < eps[j].season
		}
		if eps[i].episode != eps[j].episode {
			return eps[i].episode < eps[j].episode
		}
		return eps[i].filename < eps[j].filename
	})
	return eps
}

// enqueueBatchDownload enqueues selected episodes from a release group into the download manager.
func (m *StreamsModel) enqueueBatchDownload(mgr *player.DownloadManager, group string, downloadDir string) int {
	count := 0
	for _, ep := range m.rgEpisodes {
		if !ep.selected || ep.url == "" {
			continue
		}
		fname := ep.filename
		// A season-pack file name has no episode marker and is identical across
		// every episode, so writing them verbatim would collide on disk. Give
		// each pack episode a unique, episode-tagged name.
		if fname == "" || player.IsSeasonPack(fname) {
			fname = fmt.Sprintf("%s - %s%s", m.metaName, ep.label, player.GuessExtension(ep.url))
		} else {
			fname = player.EnsureContainer(fname, ep.url)
		}
		destPath := player.EpisodePath(downloadDir, m.metaName, m.metaYear, ep.season, fname)
		mgr.Enqueue(fname, ep.url, destPath)
		count++
	}
	return count
}

// enqueueSingleDownload enqueues a single stream download.
func (m *StreamsModel) enqueueSingleDownload(mgr *player.DownloadManager, item streamItem, downloadDir string) {
	url := item.stream.PlayableURL()
	fname := item.fileName()
	if m.contentType == "movie" {
		if fname == "" {
			fname = m.metaName
			if y := m.metaYear; y != "" {
				fname = fmt.Sprintf("%s (%s)", m.metaName, y)
			}
			fname += player.GuessExtension(url)
		} else {
			fname = player.EnsureContainer(fname, url)
		}
		destPath := player.MoviePath(downloadDir, m.metaName, m.metaYear, fname)
		mgr.Enqueue(fname, url, destPath)
		return
	}
	videoID := item.videoID
	if videoID == "" {
		videoID = m.contentID
	}
	season, episode := history.ParseEpisodeID(videoID)
	if fname == "" {
		fname = fmt.Sprintf("%s - S%02dE%02d%s", m.metaName, season, episode, player.GuessExtension(url))
	} else {
		fname = player.EnsureContainer(fname, url)
	}
	destPath := player.EpisodePath(downloadDir, m.metaName, m.metaYear, season, fname)
	mgr.Enqueue(fname, url, destPath)
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
		for i, ls := range msg.streams {
			m.allItems[i] = streamItem{stream: ls.stream}
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
		// In download mode, auto-open release group selector
		if m.downloadMode {
			m.downloadMode = false
			m.rgGroups = m.collectReleaseGroups()
			if len(m.rgGroups) > 0 {
				m.rgCursor = 0
				m.rgScroll = 0
				m.rgSelectorActive = true
			}
		}
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

	case tea.KeyMsg:
		// Episode selector popup (shown AFTER group selection)
		if m.rgEpSelectorActive {
			visible := m.popupVisibleRows()
			switch msg.String() {
			case "up", "k":
				if m.rgEpCursor > 0 {
					m.rgEpCursor--
					if m.rgEpCursor < m.rgEpScroll {
						m.rgEpScroll = m.rgEpCursor
					}
				}
			case "down", "j":
				if m.rgEpCursor < len(m.rgEpisodes)-1 {
					m.rgEpCursor++
					if m.rgEpCursor >= m.rgEpScroll+visible {
						m.rgEpScroll = m.rgEpCursor - visible + 1
					}
				}
			case " ":
				m.rgEpisodes[m.rgEpCursor].selected = !m.rgEpisodes[m.rgEpCursor].selected
			case "a":
				for i := range m.rgEpisodes {
					m.rgEpisodes[i].selected = true
				}
			case "n":
				for i := range m.rgEpisodes {
					m.rgEpisodes[i].selected = false
				}
			case "enter":
				// Enqueue downloads — handled by app.go which has access to manager
				m.rgEpSelectorActive = false
				return m, func() tea.Msg { return enqueueDownloadsMsg{} }
			case "esc":
				// Go back to group selector
				m.rgEpSelectorActive = false
				m.rgSelectorActive = true
			}
			return m, nil
		}

		// Release group selector popup (shown FIRST)
		if m.rgSelectorActive {
			visible := m.popupVisibleRows()
			switch msg.String() {
			case "up", "k":
				if m.rgCursor > 0 {
					m.rgCursor--
					if m.rgCursor < m.rgScroll {
						m.rgScroll = m.rgCursor
					}
				}
			case "down", "j":
				if m.rgCursor < len(m.rgGroups)-1 {
					m.rgCursor++
					if m.rgCursor >= m.rgScroll+visible {
						m.rgScroll = m.rgCursor - visible + 1
					}
				}
			case "enter":
				group := m.rgGroups[m.rgCursor].name
				m.rgSelectedGroup = group
				m.rgSelectorActive = false
				// Build episode list for chosen group
				m.rgEpisodes = m.collectEpisodesForGroup(group)
				m.rgEpCursor = 0
				m.rgEpScroll = 0
				m.rgEpSelectorActive = true
			case "esc":
				m.rgSelectorActive = false
				m.rgGroups = nil
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
			if m.isBatchMode() {
				// Batch mode: show release group selector first
				m.rgGroups = m.collectReleaseGroups()
				if len(m.rgGroups) == 0 {
					m.downloadMsg = "No downloadable streams (HTTP/HTTPS only)"
					return m, nil
				}
				m.rgCursor = 0
				m.rgScroll = 0
				m.rgSelectorActive = true
				return m, nil
			}
			// Single stream: enqueue directly
			if item, ok := m.list.SelectedItem().(streamItem); ok {
				url := item.stream.PlayableURL()
				if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
					m.downloadMsg = "Only HTTP/HTTPS streams can be downloaded"
					return m, nil
				}
				// Signal app to enqueue single download
				return m, func() tea.Msg {
					return enqueueSingleDownloadMsg{item: item}
				}
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
				if item.episodeLabel != "" && m.config.PlaylistEnabled() {
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

	label := func(k, v string) string {
		return DetailLabelStyle.Render(k) + DetailValueStyle.Render(v)
	}
	var rows []string

	// Release / quality line (addon's own name field, e.g. "1080p - WEB-DL - [VARYG]")
	if s.Name != "" {
		release := strings.ReplaceAll(strings.TrimSpace(s.Name), "\n", " ")
		rows = append(rows, label("Release: ", release))
	}

	// Actual file name
	if f := item.fileName(); f != "" {
		rows = append(rows, label("File:    ", f))
	}

	// Size: prefer exact behaviorHints.videoSize, else parse from text
	sizeStr := ""
	if vs := s.VideoSize(); vs > 0 {
		sizeStr = player.FormatBytes(vs)
	} else if _, disp := player.ParseSize(s.Name, s.Title, s.Description); disp != "" {
		sizeStr = disp
	}
	if sizeStr != "" {
		rows = append(rows, label("Size:    ", sizeStr))
	}

	// Source (BluRay / WEB-DL / ...)
	if src := player.ParseSource(s.Name, s.Title, s.Description); src != "" {
		rows = append(rows, label("Source:  ", src))
	}

	// Release group
	if g := player.ReleaseGroup(s.Name, item.fileName()); g != "" && g != "Unknown" {
		rows = append(rows, label("Group:   ", g))
	}

	// Source/provider tag the addon conveys (e.g. "Stremtorz TB", "RD", "Torrentio")
	if tag := providerTag(s); tag != "" {
		rows = append(rows, label("Addon:   ", tag))
	}

	// Delivery type
	rows = append(rows, label("Type:    ", streamTypeLabel(s)))

	return InfoPanelStyle.Width(m.width - 4).Render(strings.Join(rows, "\n"))
}

func (m StreamsModel) View() string {
	if m.loading {
		return m.spinner.View() + " Loading streams...\n" +
			HelpStyle.Render("esc: cancel")
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
		sections = append(sections, HelpStyle.Render("/: filter • enter: search • esc: back • q: quit"))
		return lipgloss.JoinVertical(lipgloss.Left, sections...)
	}

	view := m.list.View()
	if m.launching {
		view += "\n" + SubtitleStyle.Render("▶ Launching mpv...")
	} else if m.launched {
		view += "\n" + SubtitleStyle.Render("▶ Launched")
	}
	if m.downloadMsg != "" {
		view += "\n" + SubtitleStyle.Render(m.downloadMsg)
	}
	if m.playErr != nil {
		view += "\n" + ErrorStyle.Render(fmt.Sprintf("MPV error: %v", m.playErr))
	}
	sections = append(sections, view)
	if m.infoMode {
		sections = append(sections, m.infoPanel())
	}
	sections = append(sections, HelpStyle.Render("/: filter • c: clear • d: download • i: info • D: downloads • enter: play • esc: back • q: quit"))
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m StreamsModel) episodeSelectorView() string {
	var b strings.Builder
	b.WriteString(TitleStyle.Render(fmt.Sprintf("Episodes from %s", m.rgSelectedGroup)))
	b.WriteString("\n\n")

	visible := m.popupVisibleRows()
	end := m.rgEpScroll + visible
	if end > len(m.rgEpisodes) {
		end = len(m.rgEpisodes)
	}

	for i := m.rgEpScroll; i < end; i++ {
		ep := m.rgEpisodes[i]
		cursor := "  "
		if i == m.rgEpCursor {
			cursor = "▶ "
		}
		check := "☐"
		if ep.selected {
			check = "☑"
		}
		size := "?"
		if ep.size != "" {
			size = ep.size
		}
		// label + size, then the actual file name so alternate cuts (v2) and
		// exact contents are visible before downloading.
		head := fmt.Sprintf("%s%s %-7s %8s", cursor, check, ep.label, size)
		fname := ep.filename
		if fname != "" {
			avail := m.width - lipgloss.Width(head) - 4
			if avail < 12 {
				avail = 12
			}
			head += "  " + truncateLabel(fname, avail)
		}
		if i == m.rgEpCursor {
			b.WriteString(SubtitleStyle.Render(head))
		} else {
			b.WriteString(head)
		}
		b.WriteString("\n")
	}

	if len(m.rgEpisodes) > visible {
		b.WriteString(HelpStyle.Render(fmt.Sprintf("  (%d/%d)", m.rgEpCursor+1, len(m.rgEpisodes))))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(HelpStyle.Render("space: toggle • a: all • n: none • enter: download • esc: back"))
	return b.String()
}

func (m StreamsModel) releaseGroupSelectorView() string {
	var b strings.Builder
	b.WriteString(TitleStyle.Render("Select release group"))
	b.WriteString("\n\n")

	visible := m.popupVisibleRows()
	end := m.rgScroll + visible
	if end > len(m.rgGroups) {
		end = len(m.rgGroups)
	}

	for i := m.rgScroll; i < end; i++ {
		g := m.rgGroups[i]
		cursor := "  "
		if i == m.rgCursor {
			cursor = "▶ "
		}
		size := ""
		if g.totalSize > 0 {
			size = fmt.Sprintf(" │ ~%s", player.FormatBytes(g.totalSize))
		}
		line := fmt.Sprintf("%s%-34s │ %d eps%s", cursor, g.name, g.epCount, size)
		if i == m.rgCursor {
			b.WriteString(SubtitleStyle.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}

	if len(m.rgGroups) > visible {
		b.WriteString(HelpStyle.Render(fmt.Sprintf("  (%d/%d)", m.rgCursor+1, len(m.rgGroups))))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(HelpStyle.Render("enter: select group • esc: cancel"))
	return b.String()
}
