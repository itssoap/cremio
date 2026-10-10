package player

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidatePlayable(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"empty", "", true},
		{"option injection long", "--script=/tmp/evil.lua", true},
		{"option injection short", "-x", true},
		{"http", "http://example.com/a.mkv", false},
		{"https", "https://example.com/a.mkv", false},
		{"magnet", "magnet:?xt=urn:btih:abc", false},
		{"rtmp", "rtmp://example.com/live", false},
		{"rtmps", "rtmps://example.com/live", false},
		{"unsupported scheme ftp", "ftp://example.com/a", true},
		{"unsupported scheme file", "file:///etc/passwd", true},
		{"no scheme", "example.com/a.mkv", true},
		{"newline injection", "https://example.com/a.mkv\nC:\\secret.mkv", true},
		{"carriage return", "https://example.com/a.mkv\r", true},
		{"NUL byte", "https://example.com/a\x00.mkv", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePlayable(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePlayable(%q) err = %v, wantErr = %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

// The reported bug: a season of long addon URLs overflowed the Windows
// 32,767-character command line. The playlist now goes through a file, so what
// reaches mpv is a path, not the URLs.
func TestWritePlaylistFileHoldsLongSeason(t *testing.T) {
	var urls []string
	total := 0
	for i := 1; i <= 24; i++ {
		u := "https://aio.example/api/v1/debrid/playback/" + strings.Repeat("x", 1850) + "/" + string(rune('a'+i%26))
		urls = append(urls, u)
		total += len(u) + 1
	}
	if total < 32767 {
		t.Fatalf("test setup too small (%d) to reproduce the limit", total)
	}

	path, err := writePlaylistFile(urls)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	if !strings.HasPrefix(filepath.Base(path), playlistFilePrefix) || filepath.Ext(path) != ".m3u" {
		t.Errorf("unexpected playlist file name %q", path)
	}
	if len(path) > 500 {
		t.Errorf("path passed to mpv should be short, got %d chars", len(path))
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if lines[0] != "#EXTM3U" {
		t.Errorf("first line = %q, want #EXTM3U", lines[0])
	}
	if len(lines)-1 != len(urls) {
		t.Fatalf("playlist has %d entries, want %d", len(lines)-1, len(urls))
	}
	for i, u := range urls {
		if lines[i+1] != u {
			t.Errorf("entry %d mismatch", i)
		}
	}
}

func TestSweepStalePlaylists(t *testing.T) {
	old, err := writePlaylistFile([]string{"https://example.com/old.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := writePlaylistFile([]string{"https://example.com/fresh.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(old)
	defer os.Remove(fresh)

	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	sweepStalePlaylists()

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("stale playlist should have been removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh playlist must be kept")
	}
}

func TestPlayWithMPVPlaylistRejectsBadURLBeforeWritingFile(t *testing.T) {
	err := PlayWithMPVPlaylist([]string{"https://example.com/a.mkv", "https://example.com/b.mkv\nC:\\x"}, 0)
	if err == nil {
		t.Fatal("a URL with a newline must be rejected")
	}
}
