package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/itssoap/cremio/internal/appdir"
)

const CinemetaURL = "https://v3-cinemeta.strem.io/manifest.json"

type Config struct {
	Addons          []string `json:"addons"`
	AutoFocusSearch bool     `json:"auto_focus_search"`
	SearchAddon     string   `json:"search_addon"`
	PlaylistMode    bool     `json:"playlist_mode"`
	path            string
}

func Load() (*Config, error) {
	dir, err := appdir.Dir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "config.json")

	cfg := &Config{path: path}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.Addons = []string{CinemetaURL}
			cfg.SearchAddon = CinemetaURL
			_ = cfg.Save()
			return cfg, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
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
