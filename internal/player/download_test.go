package player

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeYear(t *testing.T) {
	cases := map[string]string{
		"2013":           "2013",
		"2008\u20132013": "2008", // en-dash range
		"2008\u20142013": "2008", // em-dash range
		"2008-2013":      "2008", // hyphen range
		"2008\u2013":     "2008", // open-ended range
		"":               "",
		"unknown":        "",
	}
	for in, want := range cases {
		if got := normalizeYear(in); got != want {
			t.Errorf("normalizeYear(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEpisodePathKeepsFilename(t *testing.T) {
	fname := "Saga of Tanya the Evil (2017) - S01E01 - 001 - The Devil [Bluray-1080p][x265]-Vodes.mkv"
	got := EpisodePath("/dl", "Saga of Tanya the Evil", "2017\u20132018", 1, fname)
	want := filepath.Join("/dl", "Saga of Tanya the Evil (2017)", "Season 01", fname)
	if got != want {
		t.Errorf("EpisodePath = %q, want %q", got, want)
	}
	// No Movies/TV Shows parent, dash chars preserved (year range collapsed).
	if strings.Contains(got, "TV Shows") || strings.Contains(got, "Movies") {
		t.Errorf("path should not contain media-type parent folder: %q", got)
	}
	if !strings.Contains(got, "-Vodes.mkv") {
		t.Errorf("release group lost from filename: %q", got)
	}
}

func TestMoviePathKeepsFilename(t *testing.T) {
	fname := "The Matrix (1999) [1080p BluRay x264]-GROUP.mkv"
	got := MoviePath("/dl", "The Matrix", "1999", fname)
	want := filepath.Join("/dl", "The Matrix (1999)", fname)
	if got != want {
		t.Errorf("MoviePath = %q, want %q", got, want)
	}
	if strings.Contains(got, "Movies") {
		t.Errorf("path should not contain Movies parent: %q", got)
	}
}

func TestParseAria2Size(t *testing.T) {
	cases := map[string]int64{
		"1.0B":     1,
		"512KiB":   512 * 1024,
		"1.0MiB":   1024 * 1024,
		"1.5GiB":   int64(1.5 * 1024 * 1024 * 1024),
		"2GiB":     2 * 1024 * 1024 * 1024,
		"garbage":  0,
	}
	for in, want := range cases {
		if got := parseAria2Size(in); got != want {
			t.Errorf("parseAria2Size(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseSource(t *testing.T) {
	cases := map[string]string{
		"1080p WEB-DL AVC":  "WEB-DL",
		"1080p BluRay x265": "BluRay",
		"720p HDTV":         "HDTV",
		"nothing here":      "",
	}
	for in, want := range cases {
		if got := ParseSource(in); got != want {
			t.Errorf("ParseSource(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilenameFromURL(t *testing.T) {
	if got := FilenameFromURL("https://host/path/My.Show.S01E01.mkv?token=abc"); got != "My.Show.S01E01.mkv" {
		t.Errorf("FilenameFromURL video = %q", got)
	}
	if got := FilenameFromURL("https://host/stream/12345"); got != "" {
		t.Errorf("FilenameFromURL non-file should be empty, got %q", got)
	}
}

func TestParseCodec(t *testing.T) {
	cases := map[string]string{
		"1080p WEB-DL x265 HEVC":  "HEVC",
		"1080p BluRay H.264 AVC":  "AVC",
		"2160p WEB-DL AV1":        "AV1",
		"1080p WEB-DL H264":       "AVC",
		"1080p WEB-DL DDP5.1":     "",
	}
	for in, want := range cases {
		if got := ParseCodec(in); got != want {
			t.Errorf("ParseCodec(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReleaseGroupFromFilename(t *testing.T) {
	cases := map[string]string{
		"The.Shawshank.1994.2160p.BluRay.REMUX.HDR.HEVC.DTS-HD.MA.5.1-TRiToN.mkv": "TRiToN",
		"Inception.2010.2160p.BluRay.DTS-HD.MA.5.1.HDR.x265-CtrlHD.mkv":           "CtrlHD",
		"The Godfather (1972) (2160p BluRay x265 HEVC Tigole) [QxR].mkv":          "QxR",
		"[FLE] Sakamoto Days - S01E01 (WEB 1080p H.264) [Dual Audio] [8107BB99].mkv": "FLE",
		"The Godfather (1972) {tmdb-238} - DV.HDR Remux-2160p.mkv":                "Unknown", // no scene group present
	}
	for file, want := range cases {
		if got := ReleaseGroup("GDrive 2160p", file); got != want {
			t.Errorf("ReleaseGroup(%q) = %q, want %q", file, got, want)
		}
	}
}

func TestIsSeasonPack(t *testing.T) {
	cases := []struct {
		filename string
		want     bool
	}{
		// Real season packs (season marker, no episode marker).
		{"The.Mentalist.S02.2008.1080p.NF.WEB-DL.DDP5.1.H.264-HHWEB", true},
		{"Breaking.Bad.Season.5.Complete.1080p.BluRay-GROUP", true},
		{"Show.S01.COMPLETE.720p", true},
		// Per-episode files (episode marker present).
		{"The Mentalist S02E01 2008 1080p NF WEB-DL DDP5 1 H 264-HHWEB.mkv", false},
		{"Show.2x05.1080p.WEB-DL.mkv", false},
		{"Show.S03E10.Red.Sky.1080p-OFT", false},
		// Not season content at all.
		{"The.Movie.2019.1080p.BluRay.x264-GROUP.mkv", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsSeasonPack(c.filename); got != c.want {
			t.Errorf("IsSeasonPack(%q) = %v, want %v", c.filename, got, c.want)
		}
	}
}
