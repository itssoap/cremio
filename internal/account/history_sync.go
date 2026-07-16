package account

import "github.com/itssoap/cremio/internal/history"

// watchedThreshold is Stremio's convention: an item is "watched" once progress
// passes 70% of its duration.
const watchedThreshold = 0.7

// isWatched reports whether a library state counts as watched.
func (s LibraryState) isWatched() bool {
	if s.FlaggedWatched != 0 {
		return true
	}
	return s.Duration > 0 && s.TimeOffset/s.Duration > watchedThreshold
}

// ApplyLibraryToHistory imports best-effort watched state from a Stremio library
// into the local history. It only ADDS: nothing already watched is toggled off.
// Returns the number of newly-marked items. The caller owns persistence (Save).
//
// This mapping is lossy by nature: Stremio's libraryItem stores a single current
// position per title, not a full per-episode log, so only the item's current
// video is imported for series.
//
// Note: the item's `removed` flag is intentionally IGNORED. In Stremio `removed`
// means "removed from the Library list", not "unwatched" - a finished title that
// the user cleared from their library still carries its watched state, and we
// want that history. (Most watched items on a real account are `removed:true`.)
func ApplyLibraryToHistory(h *history.WatchHistory, items []LibraryItem) int {
	added := 0
	for _, it := range items {
		if !it.State.isWatched() {
			continue
		}
		imdb := history.ExtractIMDBID(it.ID)
		if imdb == "" {
			continue
		}
		switch it.Type {
		case "movie":
			if !h.IsMovieWatched(imdb) {
				h.ToggleMovie(imdb)
				added++
			}
		case "series":
			season, episode := history.ParseEpisodeID(it.State.VideoID)
			if season == 0 || episode == 0 {
				continue
			}
			if !h.IsEpisodeWatched(imdb, season, episode) {
				h.ToggleEpisode(imdb, season, episode)
				added++
			}
		}
	}
	return added
}
