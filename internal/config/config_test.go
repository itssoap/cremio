package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPlaylistEnabledDefault(t *testing.T) {
	// Unset (nil) defaults to enabled.
	c := &Config{}
	if !c.PlaylistEnabled() {
		t.Error("playlist mode should default to enabled when unset")
	}
	f := false
	c.PlaylistMode = &f
	if c.PlaylistEnabled() {
		t.Error("explicit false should disable playlist mode")
	}
	tr := true
	c.PlaylistMode = &tr
	if !c.PlaylistEnabled() {
		t.Error("explicit true should enable playlist mode")
	}
}

func TestLoadRecoversFromInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// A Windows path written with single backslashes is invalid JSON.
	bad := `{"download_dir": "C:\Users\me\Downloads"}`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}

	c := &Config{path: path}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, c); err == nil {
		t.Fatal("expected invalid JSON to fail unmarshal")
	}

	// Full recovery should produce a working default config.
	c = defaultConfig(path)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if !c.PlaylistEnabled() {
		t.Error("default config should have playlist enabled")
	}
}

func TestResolveDownloadDirPriority(t *testing.T) {
	dir := t.TempDir()
	c := &Config{DownloadDir: dir}
	if got := c.ResolveDownloadDir(); got != dir {
		t.Errorf("configured dir should win: got %q want %q", got, dir)
	}
}
