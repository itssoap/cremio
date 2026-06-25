package player

import (
	"fmt"
	"os/exec"
)

// PlayWithMPV launches mpv with the given URL and any additional mpv flags.
// The call is non-blocking; mpv runs as a detached child process.
func PlayWithMPV(url string, extraArgs ...string) error {
	if url == "" {
		return fmt.Errorf("no playable URL")
	}

	args := append(extraArgs, url) //nolint:gocritic // intentional: flags before URL
	cmd := exec.Command("mpv", args...)
	return cmd.Start()
}

// PlayWithMPVPlaylist launches mpv with multiple URLs as a playlist,
// starting playback at the given index (0-based).
func PlayWithMPVPlaylist(urls []string, startIndex int, extraArgs ...string) error {
	if len(urls) == 0 {
		return fmt.Errorf("empty playlist")
	}
	if startIndex < 0 || startIndex >= len(urls) {
		startIndex = 0
	}

	args := make([]string, 0, len(extraArgs)+len(urls)+1)
	args = append(args, extraArgs...)
	args = append(args, fmt.Sprintf("--playlist-start=%d", startIndex))
	args = append(args, urls...)
	cmd := exec.Command("mpv", args...)
	return cmd.Start()
}
