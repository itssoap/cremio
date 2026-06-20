package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/itssoap/cremio/internal/appdir"
)

// WatchHistory tracks locally watched movies and episodes as JSON.
type WatchHistory struct {
	Movies []WatchedMovie `json:"movies,omitempty"`
	Shows  []WatchedShow  `json:"shows,omitempty"`
	path   string
}

type WatchedMovie struct {
	WatchedAt string   `json:"watched_at"`
	IDs       WatchIDs `json:"ids"`
}

type WatchedShow struct {
	IDs     WatchIDs        `json:"ids"`
	Seasons []WatchedSeason `json:"seasons"`
}

type WatchedSeason struct {
	Number   int              `json:"number"`
	Episodes []WatchedEpisode `json:"episodes"`
}

type WatchedEpisode struct {
	Number    int    `json:"number"`
	WatchedAt string `json:"watched_at"`
}

type WatchIDs struct {
	IMDB string `json:"imdb"`
}

func Load() (*WatchHistory, error) {
	dir, err := appdir.Dir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "history.json")

	h := &WatchHistory{path: path}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return h, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, h); err != nil {
		return nil, err
	}
	h.path = path
	return h, nil
}

func (h *WatchHistory) Save() error {
	dir := filepath.Dir(h.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(h.path, data, 0o644)
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// ExtractIMDBID extracts the content ID from a Stremio meta/video ID,
// stripping any trailing season:episode suffix.
// e.g. "tt1234567" → "tt1234567"
//
//	"tt1234567:1:2" → "tt1234567"
//	"tmdb:12345" → "tmdb:12345"
//	"tmdb:12345:1:2" → "tmdb:12345"
func ExtractIMDBID(id string) string {
	parts := strings.Split(id, ":")
	n := len(parts)
	if n >= 3 {
		var s, e int
		if _, err := fmt.Sscanf(parts[n-2]+":"+parts[n-1], "%d:%d", &s, &e); err == nil {
			return strings.Join(parts[:n-2], ":")
		}
	}
	return id
}

// ParseEpisodeID extracts the season and episode numbers from a Stremio video ID.
// Works for both "tt1234567:2:5" and "tmdb:12345:2:5" → (2, 5).
// Returns (0, 0) if the ID is not an episode format.
func ParseEpisodeID(id string) (season, episode int) {
	parts := strings.Split(id, ":")
	n := len(parts)
	if n < 3 {
		return 0, 0
	}
	_, err := fmt.Sscanf(parts[n-2]+":"+parts[n-1], "%d:%d", &season, &episode)
	if err != nil {
		return 0, 0
	}
	return season, episode
}

// IsMovieWatched returns true if a movie with the given IMDB ID is watched.
func (h *WatchHistory) IsMovieWatched(imdbID string) bool {
	for _, m := range h.Movies {
		if m.IDs.IMDB == imdbID {
			return true
		}
	}
	return false
}

// IsEpisodeWatched returns true if a specific episode is watched.
func (h *WatchHistory) IsEpisodeWatched(imdbID string, season, episode int) bool {
	for _, s := range h.Shows {
		if s.IDs.IMDB != imdbID {
			continue
		}
		for _, sn := range s.Seasons {
			if sn.Number != season {
				continue
			}
			for _, ep := range sn.Episodes {
				if ep.Number == episode {
					return true
				}
			}
		}
	}
	return false
}

// IsSeasonWatched returns true if all episodes in a season are watched.
func (h *WatchHistory) IsSeasonWatched(imdbID string, season, totalEpisodes int) bool {
	for _, s := range h.Shows {
		if s.IDs.IMDB != imdbID {
			continue
		}
		for _, sn := range s.Seasons {
			if sn.Number == season {
				return len(sn.Episodes) >= totalEpisodes
			}
		}
	}
	return false
}

// ToggleMovie toggles a movie's watched status. Returns the new watched state.
func (h *WatchHistory) ToggleMovie(imdbID string) bool {
	for i, m := range h.Movies {
		if m.IDs.IMDB == imdbID {
			h.Movies = append(h.Movies[:i], h.Movies[i+1:]...)
			return false
		}
	}
	h.Movies = append(h.Movies, WatchedMovie{
		WatchedAt: now(),
		IDs:       WatchIDs{IMDB: imdbID},
	})
	return true
}

// ToggleEpisode toggles an episode's watched status. Returns the new watched state.
func (h *WatchHistory) ToggleEpisode(imdbID string, season, episode int) bool {
	// Find or create show entry
	var show *WatchedShow
	for i := range h.Shows {
		if h.Shows[i].IDs.IMDB == imdbID {
			show = &h.Shows[i]
			break
		}
	}

	if show == nil {
		h.Shows = append(h.Shows, WatchedShow{
			IDs: WatchIDs{IMDB: imdbID},
		})
		show = &h.Shows[len(h.Shows)-1]
	}

	// Find or create season entry
	var sn *WatchedSeason
	for i := range show.Seasons {
		if show.Seasons[i].Number == season {
			sn = &show.Seasons[i]
			break
		}
	}

	if sn == nil {
		show.Seasons = append(show.Seasons, WatchedSeason{Number: season})
		sn = &show.Seasons[len(show.Seasons)-1]
	}

	// Toggle episode
	for i, ep := range sn.Episodes {
		if ep.Number == episode {
			sn.Episodes = append(sn.Episodes[:i], sn.Episodes[i+1:]...)
			h.cleanupEmpty()
			return false
		}
	}

	sn.Episodes = append(sn.Episodes, WatchedEpisode{
		Number:    episode,
		WatchedAt: now(),
	})
	return true
}

// ToggleSeason marks all episodes in a season as watched, or removes them all
// if the season is already fully watched. episodeNumbers should list all
// episode numbers in the season.
func (h *WatchHistory) ToggleSeason(imdbID string, season int, episodeNumbers []int) bool {
	if len(episodeNumbers) == 0 {
		return false
	}
	if h.IsSeasonWatched(imdbID, season, len(episodeNumbers)) {
		// Unwatch: remove all episodes in this season
		for i := range h.Shows {
			if h.Shows[i].IDs.IMDB != imdbID {
				continue
			}
			for j, sn := range h.Shows[i].Seasons {
				if sn.Number == season {
					h.Shows[i].Seasons = append(h.Shows[i].Seasons[:j], h.Shows[i].Seasons[j+1:]...)
					break
				}
			}
		}
		h.cleanupEmpty()
		return false
	}
	// Watch: mark all missing episodes as watched
	for _, ep := range episodeNumbers {
		if !h.IsEpisodeWatched(imdbID, season, ep) {
			h.ToggleEpisode(imdbID, season, ep)
		}
	}
	return true
}

// cleanupEmpty removes shows/seasons with no episodes.
func (h *WatchHistory) cleanupEmpty() {
	var shows []WatchedShow
	for _, s := range h.Shows {
		var seasons []WatchedSeason
		for _, sn := range s.Seasons {
			if len(sn.Episodes) > 0 {
				seasons = append(seasons, sn)
			}
		}
		if len(seasons) > 0 {
			s.Seasons = seasons
			shows = append(shows, s)
		}
	}
	h.Shows = shows
}
