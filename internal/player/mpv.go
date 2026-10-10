package player

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// allowedSchemes lists the URL schemes cremio is willing to hand to mpv.
// Stream URLs originate from untrusted third-party addons, so anything
// outside this set (including option-like values starting with "-") is
// rejected before launch.
var allowedSchemes = []string{"http://", "https://", "rtmp://", "rtmps://", "magnet:"}

// validatePlayable rejects empty values, option-injection attempts (values
// beginning with "-", which mpv would parse as a flag), control characters
// (a newline would inject extra entries into a playlist file) and unknown
// schemes.
func validatePlayable(url string) error {
	if url == "" {
		return fmt.Errorf("no playable URL")
	}
	if strings.HasPrefix(url, "-") {
		return fmt.Errorf("refusing to play option-like URL %q", url)
	}
	if strings.IndexFunc(url, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return fmt.Errorf("refusing to play URL containing control characters")
	}
	for _, s := range allowedSchemes {
		if strings.HasPrefix(url, s) {
			return nil
		}
	}
	return fmt.Errorf("refusing to play URL with unsupported scheme: %q", url)
}

// PlayWithMPV launches mpv with the given URL and any additional mpv flags.
// The call is non-blocking; mpv runs as a detached child process.
func PlayWithMPV(url string, extraArgs ...string) error {
	if err := validatePlayable(url); err != nil {
		return err
	}

	// "--" terminates option parsing so a malicious addon cannot smuggle
	// mpv flags through the stream URL.
	args := make([]string, 0, len(extraArgs)+2)
	args = append(args, extraArgs...)
	args = append(args, "--", url)
	cmd := exec.Command("mpv", args...)
	return cmd.Start()
}

// playlistFilePrefix names the temporary playlist files so stale ones left by
// a crash can be recognised and swept.
const playlistFilePrefix = "cremio-playlist-"

// sweepStalePlaylists removes playlist files older than a day (left behind if
// cremio was killed before it could clean up after mpv).
func sweepStalePlaylists() {
	old, _ := filepath.Glob(filepath.Join(os.TempDir(), playlistFilePrefix+"*.m3u"))
	for _, p := range old {
		if fi, err := os.Stat(p); err == nil && time.Since(fi.ModTime()) > 24*time.Hour {
			_ = os.Remove(p)
		}
	}
}

// writePlaylistFile writes urls, one per line, to a new private (0600) temp
// .m3u file and returns its path. The URLs can carry debrid tokens, so the file
// is short-lived and removed by the caller once mpv exits.
func writePlaylistFile(urls []string) (string, error) {
	f, err := os.CreateTemp("", playlistFilePrefix+"*.m3u")
	if err != nil {
		return "", fmt.Errorf("create playlist file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString("#EXTM3U\n" + strings.Join(urls, "\n") + "\n"); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("write playlist file: %w", err)
	}
	return f.Name(), nil
}

// PlayWithMPVPlaylist launches mpv with multiple URLs as a playlist,
// starting playback at the given index (0-based).
//
// The URLs are handed to mpv through a temporary playlist file, not as
// command-line arguments: addon URLs can be ~2000 characters each (encrypted
// debrid tokens), so a season easily exceeds the Windows 32,767-character
// command-line limit ("The filename or extension is too long").
func PlayWithMPVPlaylist(urls []string, startIndex int, extraArgs ...string) error {
	if len(urls) == 0 {
		return fmt.Errorf("empty playlist")
	}
	if startIndex < 0 || startIndex >= len(urls) {
		startIndex = 0
	}
	for _, url := range urls {
		if err := validatePlayable(url); err != nil {
			return err
		}
	}

	sweepStalePlaylists()
	playlist, err := writePlaylistFile(urls)
	if err != nil {
		return err
	}

	args := make([]string, 0, len(extraArgs)+2)
	args = append(args, extraArgs...)
	args = append(args, fmt.Sprintf("--playlist-start=%d", startIndex), "--playlist="+playlist)
	cmd := exec.Command("mpv", args...)
	if err := cmd.Start(); err != nil {
		_ = os.Remove(playlist)
		return err
	}
	// Remove the file (and reap the process) once mpv exits.
	go func() {
		_ = cmd.Wait()
		_ = os.Remove(playlist)
	}()
	return nil
}
