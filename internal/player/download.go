package player

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// DownloadProgress is sent on the progress channel during downloads.
type DownloadProgress struct {
	Label      string // e.g. "S01E05"
	BytesRead  int64
	TotalBytes int64  // -1 if unknown
	Done       bool
	Err        error
	Skipped    bool
	SkipReason string
}

// sanitizePath removes characters that are invalid in file/directory names.
func sanitizePath(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			b.WriteRune('_')
		default:
			if unicode.IsControl(r) {
				continue
			}
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// PlexMoviePath returns: <base>/Movies/<Name> (<Year>)/<Name> (<Year>).<ext>
func PlexMoviePath(base, name, year, ext string) string {
	folder := sanitizePath(name)
	if year != "" {
		folder = fmt.Sprintf("%s (%s)", sanitizePath(name), year)
	}
	filename := folder + ext
	return filepath.Join(base, "Movies", folder, filename)
}

// PlexEpisodePath returns: <base>/TV Shows/<Name> (<Year>)/Season <SS>/<Name> (<Year>) - S<SS>E<EE> - <EpTitle>.<ext>
func PlexEpisodePath(base, showName, year string, season, episode int, epTitle, ext string) string {
	show := sanitizePath(showName)
	if year != "" {
		show = fmt.Sprintf("%s (%s)", sanitizePath(showName), year)
	}
	seasonDir := fmt.Sprintf("Season %02d", season)
	filename := fmt.Sprintf("%s - S%02dE%02d", show, season, episode)
	if epTitle != "" {
		filename += " - " + sanitizePath(epTitle)
	}
	filename += ext
	return filepath.Join(base, "TV Shows", show, seasonDir, filename)
}

// GuessExtension tries to extract a file extension from a URL or falls back to .mkv.
func GuessExtension(url string) string {
	// Strip query/fragment
	clean := strings.SplitN(url, "?", 2)[0]
	clean = strings.SplitN(clean, "#", 2)[0]
	ext := filepath.Ext(clean)
	switch strings.ToLower(ext) {
	case ".mkv", ".mp4", ".avi", ".webm", ".ts":
		return ext
	}
	return ".mkv"
}

// ExtractReleaseGroup tries to pull the release group from a stream name.
// Typical patterns: "...-GroupName" or "...[GroupName]"
// Falls back to the full addon name (first line of stream.Name).
func ExtractReleaseGroup(streamName string) string {
	name := strings.TrimSpace(streamName)
	if name == "" {
		return "Unknown"
	}

	// First line is typically the addon/source name
	lines := strings.SplitN(name, "\n", 2)
	firstLine := strings.TrimSpace(lines[0])

	// Try to find release group in the full name: "-GroupName" at end
	re := regexp.MustCompile(`-([A-Za-z0-9]+)(?:\s*$|\s*\n)`)
	if matches := re.FindStringSubmatch(name); len(matches) > 1 {
		return matches[1]
	}

	// Try bracket notation: [GroupName]
	re2 := regexp.MustCompile(`\[([A-Za-z0-9]+)\]`)
	if matches := re2.FindStringSubmatch(name); len(matches) > 1 {
		return matches[1]
	}

	return firstLine
}

// DownloadHTTP downloads an HTTP(S) URL to destPath, reporting progress.
func DownloadHTTP(ctx context.Context, url, destPath string) (*DownloadProgress, error) {
	if err := validatePlayable(url); err != nil {
		return nil, err
	}

	// Only HTTP(S) downloads are supported
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("only HTTP/HTTPS streams can be downloaded, got: %s", url)
	}

	// Create parent directories
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	// Check if file already exists
	if _, err := os.Stat(destPath); err == nil {
		return &DownloadProgress{Done: true, Skipped: true, SkipReason: "file already exists"}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 0} // no timeout for large downloads
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	tmpPath := destPath + ".part"
	f, err := os.Create(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("create file: %w", err)
	}

	written, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("download: %w", err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return nil, fmt.Errorf("rename: %w", err)
	}

	return &DownloadProgress{
		BytesRead:  written,
		TotalBytes: resp.ContentLength,
		Done:       true,
	}, nil
}

// DownloadResult is the message sent back to the TUI after a download completes.
type DownloadResult struct {
	Label    string
	Path     string
	Err      error
	Skipped  bool
	SkipMsg  string
	Duration time.Duration
}

// BatchDownloadResult is sent when an entire batch finishes.
type BatchDownloadResult struct {
	Results []DownloadResult
}
