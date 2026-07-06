package player

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeYear(t *testing.T) {
	cases := map[string]string{
		"2013":            "2013",
		"2008\u20132013":  "2008", // en-dash range
		"2008\u20142013":  "2008", // em-dash range
		"2008-2013":       "2008", // hyphen range
		"2008\u2013":      "2008", // open-ended range
		"":                "",
		"unknown":         "",
	}
	for in, want := range cases {
		if got := normalizeYear(in); got != want {
			t.Errorf("normalizeYear(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlexEpisodePathYearRange(t *testing.T) {
	got := PlexEpisodePath("/dl", "The Show", "2008\u20132013", 1, 2, "Pilot", ".mkv")
	want := filepath.Join("/dl", "TV Shows", "The Show (2008)", "Season 01", "The Show (2008) - S01E02 - Pilot.mkv")
	if got != want {
		t.Errorf("PlexEpisodePath = %q, want %q", got, want)
	}
	if strings.ContainsRune(got, '\u2013') || strings.ContainsRune(got, '\u2014') {
		t.Errorf("path contains dash char: %q", got)
	}
}
