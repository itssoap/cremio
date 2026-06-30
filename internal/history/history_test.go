package history

import "testing"

func TestExtractIMDBID(t *testing.T) {
	tests := map[string]string{
		"tt1234567":       "tt1234567",
		"tt1234567:1:2":   "tt1234567",
		"tmdb:12345":      "tmdb:12345",
		"tmdb:12345:1:2":  "tmdb:12345",
		"tt1:notnum:here": "tt1:notnum:here",
	}
	for in, want := range tests {
		if got := ExtractIMDBID(in); got != want {
			t.Errorf("ExtractIMDBID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseEpisodeID(t *testing.T) {
	tests := []struct {
		id            string
		season, episo int
	}{
		{"tt1234567:2:5", 2, 5},
		{"tmdb:12345:2:5", 2, 5},
		{"tt1234567", 0, 0},
		{"tmdb:12345", 0, 0},
	}
	for _, tt := range tests {
		s, e := ParseEpisodeID(tt.id)
		if s != tt.season || e != tt.episo {
			t.Errorf("ParseEpisodeID(%q) = (%d,%d), want (%d,%d)", tt.id, s, e, tt.season, tt.episo)
		}
	}
}

func TestToggleMovie(t *testing.T) {
	h := &WatchHistory{}
	if h.IsMovieWatched("tt1") {
		t.Fatal("movie should start unwatched")
	}
	if !h.ToggleMovie("tt1") {
		t.Error("first toggle should mark watched")
	}
	if !h.IsMovieWatched("tt1") {
		t.Error("movie should be watched after toggle")
	}
	if h.ToggleMovie("tt1") {
		t.Error("second toggle should unmark watched")
	}
	if h.IsMovieWatched("tt1") {
		t.Error("movie should be unwatched after second toggle")
	}
}

func TestToggleEpisodeAndCleanup(t *testing.T) {
	h := &WatchHistory{}
	if !h.ToggleEpisode("tt1", 1, 1) {
		t.Error("first episode toggle should mark watched")
	}
	if !h.IsEpisodeWatched("tt1", 1, 1) {
		t.Error("episode should be watched")
	}
	if h.ToggleEpisode("tt1", 1, 1) {
		t.Error("second episode toggle should unmark watched")
	}
	if len(h.Shows) != 0 {
		t.Errorf("empty show should be cleaned up, got %d shows", len(h.Shows))
	}
}

func TestSeasonWatched(t *testing.T) {
	h := &WatchHistory{}
	eps := []int{1, 2, 3}
	if h.IsSeasonWatched("tt1", 1, len(eps)) {
		t.Fatal("season should start unwatched")
	}
	if !h.ToggleSeason("tt1", 1, eps) {
		t.Error("toggling season should mark it watched")
	}
	if !h.IsSeasonWatched("tt1", 1, len(eps)) {
		t.Error("season should be fully watched")
	}
	if h.ToggleSeason("tt1", 1, eps) {
		t.Error("toggling a watched season should unmark it")
	}
	if h.IsSeasonWatched("tt1", 1, len(eps)) {
		t.Error("season should be unwatched after second toggle")
	}
}
