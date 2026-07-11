package tui

import (
	"testing"

	"github.com/itssoap/cremio/internal/config"
	"github.com/itssoap/cremio/internal/stremio"
)

func TestParseFilterAndMatch(t *testing.T) {
	f := ParseFilter("+1080p web -cam")
	if f.IsEmpty() {
		t.Fatal("expected non-empty filter")
	}
	if !f.Match("Movie 1080p WEB-DL") {
		t.Error("expected match for included keywords")
	}
	if f.Match("Movie 1080p CAM") {
		t.Error("expected no match when an exclude keyword is present")
	}
	if f.Match("Movie 720p web") {
		t.Error("expected no match when an include keyword is missing")
	}
}

func TestParseFilterEmpty(t *testing.T) {
	if !ParseFilter("").IsEmpty() {
		t.Error("empty input should produce an empty filter")
	}
	if !ParseFilter("   +  -  ").IsEmpty() {
		t.Error("bare prefixes should not create keywords")
	}
}

func TestFilterMatchCaseInsensitive(t *testing.T) {
	f := ParseFilter("HDR")
	if !f.Match("movie hdr10") {
		t.Error("matching should be case-insensitive")
	}
}

func TestFilterMatchAcrossFields(t *testing.T) {
	f := ParseFilter("5gb")
	if !f.Match("Some Addon", "1080p 5GB seeders:50") {
		t.Error("expected match across combined fields")
	}
}

func TestPassesGlobalFilters(t *testing.T) {
	item := streamItem{
		stream: stremioStream("2160p - WEB-DL - HEVC - [VARYG]",
			"Show.S01E01.2160p.WEB-DL-VARYG.mkv", "http://host/f.mkv"),
	}
	item.stream.Description = "\U0001F4C1 Show.S01E01.2160p.WEB-DL-VARYG.mkv \nSize: 5 GB\nAddon : Strem Torz | RD"
	pass := func(g config.GlobalFilters) bool { return passesGlobalFilters(g, item) }

	if !pass(config.GlobalFilters{}) {
		t.Error("empty global filters should pass")
	}
	if !pass(config.GlobalFilters{Addon: "strem torz"}) {
		t.Error("addon filter should match the provider tag from the description")
	}
	if !pass(config.GlobalFilters{Addon: "RD"}) {
		t.Error("addon filter should match the debrid tag")
	}
	if pass(config.GlobalFilters{Addon: "torbox"}) {
		t.Error("addon filter should reject a non-matching provider tag")
	}
	if !pass(config.GlobalFilters{FileSource: "web-dl"}) {
		t.Error("source filter should match WEB-DL")
	}
	if pass(config.GlobalFilters{FileSource: "bluray"}) {
		t.Error("source filter should reject when source differs")
	}
	if !pass(config.GlobalFilters{Type: "http"}) {
		t.Error("type filter should match HTTP")
	}
	if !pass(config.GlobalFilters{ReleaseGroup: "varyg"}) {
		t.Error("release group filter should match VARYG")
	}
	if pass(config.GlobalFilters{ReleaseGroup: "-varyg"}) {
		t.Error("exclude release group should reject VARYG")
	}
}

func stremioStream(name, filename, url string) stremio.Stream {
	return stremio.Stream{
		Name:          name,
		URL:           url,
		BehaviorHints: &stremio.StreamBehaviorHints{Filename: filename},
	}
}
