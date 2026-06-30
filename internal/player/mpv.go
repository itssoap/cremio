package player

import (
	"fmt"
	"os/exec"
	"strings"
)

// allowedSchemes lists the URL schemes cremio is willing to hand to mpv.
// Stream URLs originate from untrusted third-party addons, so anything
// outside this set (including option-like values starting with "-") is
// rejected before launch.
var allowedSchemes = []string{"http://", "https://", "rtmp://", "rtmps://", "magnet:"}

// validatePlayable rejects empty values, option-injection attempts (values
// beginning with "-", which mpv would parse as a flag) and unknown schemes.
func validatePlayable(url string) error {
	if url == "" {
		return fmt.Errorf("no playable URL")
	}
	if strings.HasPrefix(url, "-") {
		return fmt.Errorf("refusing to play option-like URL %q", url)
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

// PlayWithMPVPlaylist launches mpv with multiple URLs as a playlist,
// starting playback at the given index (0-based).
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

	args := make([]string, 0, len(extraArgs)+len(urls)+2)
	args = append(args, extraArgs...)
	args = append(args, fmt.Sprintf("--playlist-start=%d", startIndex))
	// "--" terminates option parsing; every following entry is treated as a
	// positional URL rather than an mpv flag.
	args = append(args, "--")
	args = append(args, urls...)
	cmd := exec.Command("mpv", args...)
	return cmd.Start()
}
