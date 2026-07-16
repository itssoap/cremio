package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"

	"github.com/itssoap/cremio/internal/appdir"
)

const CinemetaURL = "https://v3-cinemeta.strem.io/manifest.json"

// GlobalFilters holds persistent include/exclude filter expressions applied to
// every stream cremio outputs. Each field uses the same "+include -exclude"
// syntax as the in-view stream filter. Empty fields are ignored.
type GlobalFilters struct {
	Addon        string `json:"addon"`
	FileInfo     string `json:"file_info"`
	FileSource   string `json:"file_source"`
	Type         string `json:"type"`
	ReleaseGroup string `json:"release_group"`
}

// IsEmpty reports whether no global filter is configured.
func (g GlobalFilters) IsEmpty() bool {
	return g.Addon == "" && g.FileInfo == "" && g.FileSource == "" &&
		g.Type == "" && g.ReleaseGroup == ""
}

// AccountConfig holds Stremio account sync settings. The session token itself
// is NEVER stored here; it lives in a separate 0600 auth.json file (see the
// account package). Email is display-only. Passwords are never persisted.
type AccountConfig struct {
	Enabled     bool   `json:"enabled"`
	SyncAddons  bool   `json:"sync_addons"`
	SyncHistory bool   `json:"sync_history"`
	SyncWrite   bool   `json:"sync_write"`
	Email       string `json:"email,omitempty"`
}

type Config struct {
	Addons           []string      `json:"addons"`
	AutoFocusSearch  bool          `json:"auto_focus_search"`
	SearchAddon      string        `json:"search_addon"`
	PlaylistMode     *bool         `json:"playlist_mode,omitempty"`
	GlobalFilters    GlobalFilters `json:"global_filters"`
	DownloadDir      string        `json:"download_dir"`
	DownloadAria2c   *bool         `json:"download_use_aria2c,omitempty"`
	DownloadParallel int           `json:"download_parallel,omitempty"`
	Account          AccountConfig `json:"account"`
	path             string
}

// PlaylistEnabled reports whether playlist mode is on. It defaults to true when
// the field is unset, so batch playback is the default behaviour.
func (c *Config) PlaylistEnabled() bool {
	return c.PlaylistMode == nil || *c.PlaylistMode
}

func defaultConfig(path string) *Config {
	return &Config{
		path:        path,
		Addons:      []string{CinemetaURL},
		SearchAddon: CinemetaURL,
	}
}

func Load() (*Config, error) {
	dir, err := appdir.Dir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "config.json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := defaultConfig(path)
			_ = cfg.Save()
			return cfg, nil
		}
		return nil, err
	}

	cfg := &Config{path: path}
	if err := json.Unmarshal(data, cfg); err != nil {
		// A malformed config (e.g. a Windows path written with single
		// backslashes, which is invalid JSON) must not crash the app. Back up
		// the bad file and start from defaults so cremio still launches.
		_ = os.Rename(path, path+".invalid")
		cfg = defaultConfig(path)
		_ = cfg.Save()
		return cfg, nil
	}
	if cfg.SearchAddon == "" {
		cfg.SearchAddon = CinemetaURL
	}
	return cfg, nil
}

func (c *Config) Save() error {
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(c.path, data, 0o600)
}

// ResolveDownloadDir returns the base directory for downloads using the
// priority: configured download_dir > OS Downloads folder > current directory.
// The chosen directory is created if it does not exist; on any failure it falls
// through to the next candidate, and finally to ".".
func (c *Config) ResolveDownloadDir() string {
	candidates := []string{}
	if c.DownloadDir != "" {
		candidates = append(candidates, c.DownloadDir)
	}
	if d := osDownloadsDir(); d != "" {
		candidates = append(candidates, d)
	}
	for _, dir := range candidates {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			return dir
		}
	}
	return "."
}

// osDownloadsDir returns the operating system's default Downloads folder, or ""
// if it cannot be determined.
func osDownloadsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	dl := filepath.Join(home, "Downloads")
	if info, err := os.Stat(dl); err == nil && info.IsDir() {
		return dl
	}
	// On some systems the folder may not exist yet; still prefer it over CWD.
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		return dl
	}
	return ""
}

func (c *Config) AddAddon(url string) {
	for _, a := range c.Addons {
		if a == url {
			return
		}
	}
	c.Addons = append(c.Addons, url)
}

func (c *Config) RemoveAddon(url string) {
	for i, a := range c.Addons {
		if a == url {
			c.Addons = append(c.Addons[:i], c.Addons[i+1:]...)
			return
		}
	}
}
