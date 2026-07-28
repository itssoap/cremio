package account

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/itssoap/cremio/internal/appdir"
)

// authFileName is where the session token is persisted, separate from config so
// it is easy to revoke locally and easy to keep out of shared config dumps.
const authFileName = "auth.json"

type authFile struct {
	AuthKey string `json:"auth_key"`
}

func authPath() (string, error) {
	dir, err := appdir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, authFileName), nil
}

// LoadAuthKey reads the persisted session token. It returns "" (and a nil error)
// when no auth file exists.
func LoadAuthKey() (string, error) {
	path, err := authPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var af authFile
	if err := json.Unmarshal(data, &af); err != nil {
		// A corrupt auth file should not crash the app; treat as logged out.
		return "", nil
	}
	return af.AuthKey, nil
}

// SaveAuthKey persists the session token with 0600 permissions.
func SaveAuthKey(key string) error {
	path, err := authPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(authFile{AuthKey: key}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// DeleteAuthKey removes the persisted session token (local logout).
func DeleteAuthKey() error {
	path, err := authPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
