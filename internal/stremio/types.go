package stremio

import (
	"encoding/json"
	"fmt"
)

// FlexString handles JSON values that may be either a string or a number,
// normalizing them to a string. Some addons (e.g. TVDB) return numeric
// imdbRating while others (Cinemeta) return a string.
type FlexString string

func (f *FlexString) UnmarshalJSON(data []byte) error {
	// Try string first
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*f = FlexString(s)
		return nil
	}
	// Try number
	var n float64
	if err := json.Unmarshal(data, &n); err == nil {
		*f = FlexString(fmt.Sprintf("%.1f", n))
		return nil
	}
	// Null or unrecognized — leave empty
	*f = ""
	return nil
}

func (f FlexString) String() string { return string(f) }

type Manifest struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Version     string    `json:"version"`
	Resources   []any     `json:"resources"`
	Types       []string  `json:"types"`
	Catalogs    []Catalog `json:"catalogs"`
	IDPrefixes  []string  `json:"idPrefixes,omitempty"`
}

type Catalog struct {
	Type  string         `json:"type"`
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Extra []CatalogExtra `json:"extra,omitempty"`
}

type CatalogExtra struct {
	Name       string   `json:"name"`
	IsRequired bool     `json:"isRequired,omitempty"`
	Options    []string `json:"options,omitempty"`
}

func (c Catalog) SupportsSearch() bool {
	for _, e := range c.Extra {
		if e.Name == "search" {
			return true
		}
	}
	return false
}

type CatalogResponse struct {
	Metas []MetaPreview `json:"metas"`
}

type MetaPreview struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Poster      string `json:"poster,omitempty"`
	PosterShape string `json:"posterShape,omitempty"`
	Description string `json:"description,omitempty"`
	ReleaseInfo string `json:"releaseInfo,omitempty"`
	IMDBRating  FlexString `json:"imdbRating,omitempty"`
}

type MetaResponse struct {
	Meta Meta `json:"meta"`
}

type Meta struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	Name        string     `json:"name"`
	Poster      string     `json:"poster,omitempty"`
	Background  string     `json:"background,omitempty"`
	Description string     `json:"description,omitempty"`
	ReleaseInfo string     `json:"releaseInfo,omitempty"`
	IMDBRating  FlexString `json:"imdbRating,omitempty"`
	IMDBID      string     `json:"imdb_id,omitempty"`
	Runtime     string   `json:"runtime,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	Director    []string `json:"director,omitempty"`
	Cast        []string `json:"cast,omitempty"`
	Videos      []Video  `json:"videos,omitempty"`
}

type Video struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Name      string `json:"name,omitempty"`
	Released  string `json:"released,omitempty"`
	Season    int    `json:"season,omitempty"`
	Episode   int    `json:"episode,omitempty"`
	Overview  string `json:"overview,omitempty"`
	Thumbnail string `json:"thumbnail,omitempty"`
}

// DisplayTitle returns the video title, preferring Title over Name.
func (v Video) DisplayTitle() string {
	if v.Title != "" {
		return v.Title
	}
	return v.Name
}

type StreamResponse struct {
	Streams []Stream `json:"streams"`
}

type Stream struct {
	URL           string               `json:"url,omitempty"`
	YtID          string               `json:"ytId,omitempty"`
	InfoHash      string               `json:"infoHash,omitempty"`
	FileIdx       *int                 `json:"fileIdx,omitempty"`
	ExternalURL   string               `json:"externalUrl,omitempty"`
	Name          string               `json:"name,omitempty"`
	Title         string               `json:"title,omitempty"`
	Description   string               `json:"description,omitempty"`
	BehaviorHints *StreamBehaviorHints `json:"behaviorHints,omitempty"`
}

type StreamBehaviorHints struct {
	NotWebReady  bool          `json:"notWebReady,omitempty"`
	BingeGroup   string        `json:"bingeGroup,omitempty"`
	Filename     string        `json:"filename,omitempty"`
	VideoSize    int64         `json:"videoSize,omitempty"`
	ProxyHeaders *ProxyHeaders `json:"proxyHeaders,omitempty"`
}

// Filename returns the addon-provided real filename for the stream, if any.
func (s Stream) Filename() string {
	if s.BehaviorHints != nil {
		return s.BehaviorHints.Filename
	}
	return ""
}

// VideoSize returns the addon-provided exact file size in bytes, or 0.
func (s Stream) VideoSize() int64 {
	if s.BehaviorHints != nil {
		return s.BehaviorHints.VideoSize
	}
	return 0
}

type ProxyHeaders struct {
	Request  map[string]string `json:"request,omitempty"`
	Response map[string]string `json:"response,omitempty"`
}

func (s Stream) PlayableURL() string {
	if s.URL != "" {
		return s.URL
	}
	if s.InfoHash != "" {
		magnet := "magnet:?xt=urn:btih:" + s.InfoHash
		if s.FileIdx != nil {
			magnet += fmt.Sprintf("&so=%d", *s.FileIdx)
		}
		return magnet
	}
	if s.YtID != "" {
		return "https://www.youtube.com/watch?v=" + s.YtID
	}
	if s.ExternalURL != "" {
		return s.ExternalURL
	}
	return ""
}

func (s Stream) DisplayName() string {
	if s.Name != "" {
		return s.Name
	}
	if s.Title != "" {
		return s.Title
	}
	if s.Description != "" {
		return s.Description
	}
	if s.URL != "" {
		return "HTTP Stream"
	}
	if s.InfoHash != "" {
		return "Torrent: " + s.InfoHash[:8] + "..."
	}
	return "Unknown Stream"
}
