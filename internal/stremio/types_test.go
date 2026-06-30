package stremio

import (
	"encoding/json"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	tests := map[string]string{
		"https://x.io/manifest.json":  "https://x.io",
		"https://x.io/":               "https://x.io",
		"https://x.io":                "https://x.io",
		"https://x.io/manifest.json/": "https://x.io",
	}
	for in, want := range tests {
		if got := NormalizeBaseURL(in); got != want {
			t.Errorf("NormalizeBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFlexStringUnmarshal(t *testing.T) {
	tests := map[string]string{
		`"7.5"`: "7.5",
		`8`:     "8.0",
		`8.3`:   "8.3",
		`null`:  "",
	}
	for in, want := range tests {
		var f FlexString
		if err := json.Unmarshal([]byte(in), &f); err != nil {
			t.Fatalf("Unmarshal(%s) error: %v", in, err)
		}
		if f.String() != want {
			t.Errorf("FlexString(%s) = %q, want %q", in, f.String(), want)
		}
	}
}

func TestStreamPlayableURL(t *testing.T) {
	idx := 3
	tests := []struct {
		name   string
		stream Stream
		want   string
	}{
		{"http", Stream{URL: "http://x/a.mkv"}, "http://x/a.mkv"},
		{"infohash", Stream{InfoHash: "abc"}, "magnet:?xt=urn:btih:abc"},
		{"infohash with file", Stream{InfoHash: "abc", FileIdx: &idx}, "magnet:?xt=urn:btih:abc&so=3"},
		{"youtube", Stream{YtID: "xyz"}, "https://www.youtube.com/watch?v=xyz"},
		{"external", Stream{ExternalURL: "http://ext"}, "http://ext"},
		{"none", Stream{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.stream.PlayableURL(); got != tt.want {
				t.Errorf("PlayableURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCatalogSupportsSearch(t *testing.T) {
	withSearch := Catalog{Extra: []CatalogExtra{{Name: "search"}}}
	if !withSearch.SupportsSearch() {
		t.Error("catalog with search extra should support search")
	}
	without := Catalog{Extra: []CatalogExtra{{Name: "genre"}}}
	if without.SupportsSearch() {
		t.Error("catalog without search extra should not support search")
	}
}
