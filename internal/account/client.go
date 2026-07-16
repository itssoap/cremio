// Package account talks to Stremio's official account API (https://api.strem.io)
// so cremio users can log in with their existing Stremio account and pull the
// addon collection (and, best-effort, watch history) that Stremio and Nuvio
// already sync for them.
//
// Security: this package stores ONLY the session authKey, never a password. The
// authKey is a bearer token and is persisted with 0600 permissions. Nothing here
// logs the token or the password.
package account

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// DefaultEndpoint is the Stremio account API root.
const DefaultEndpoint = "https://api.strem.io"

// Client is a stateless-ish Stremio API client. The only mutable state is the
// authKey, guarded by a mutex because login/logout/sync commands run in
// concurrent bubbletea goroutines.
type Client struct {
	endpoint string
	http     *http.Client

	mu      sync.RWMutex
	authKey string
}

// New returns a Client pointed at the default Stremio endpoint.
func New() *Client {
	return &Client{
		endpoint: DefaultEndpoint,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
}

// newWithEndpoint is used by tests to point the client at an httptest server.
func newWithEndpoint(endpoint string) *Client {
	c := New()
	c.endpoint = endpoint
	return c
}

// SetAuthKey sets the session token (e.g. loaded from disk or pasted by the user).
func (c *Client) SetAuthKey(key string) {
	c.mu.Lock()
	c.authKey = key
	c.mu.Unlock()
}

// AuthKey returns the current session token.
func (c *Client) AuthKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.authKey
}

// LoggedIn reports whether a session token is present.
func (c *Client) LoggedIn() bool { return c.AuthKey() != "" }

// User is the subset of the Stremio user object cremio uses.
type User struct {
	ID    string `json:"_id"`
	Email string `json:"email"`
}

// apiError mirrors the Stremio envelope error object.
type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// envelope is the Stremio response wrapper: {"result": ..., "error": ...}.
type envelope struct {
	Result json.RawMessage `json:"result"`
	Error  *apiError       `json:"error"`
}

// request POSTs params as JSON to /api/<method>, unwraps the envelope, and
// decodes result into out. A non-null error object becomes a Go error. params
// is treated as caller-owned; authed callers add authKey themselves.
func (c *Client) request(ctx context.Context, method string, params map[string]any, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal %s params: %w", method, err)
	}
	url := c.endpoint + "/api/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s request failed: %w", method, err)
	}
	defer resp.Body.Close()

	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	if env.Error != nil {
		if env.Error.Code != 0 {
			return fmt.Errorf("stremio: %s (code %d)", env.Error.Message, env.Error.Code)
		}
		return fmt.Errorf("stremio: %s", env.Error.Message)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s: unexpected status %d", method, resp.StatusCode)
	}
	if out == nil || len(env.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("unmarshal %s result: %w", method, err)
	}
	return nil
}

// authResult covers both {"authKey":...} and the legacy {"key":...} spelling.
type authResult struct {
	AuthKey string `json:"authKey"`
	Key     string `json:"key"`
	User    User   `json:"user"`
}

func (a authResult) token() string {
	if a.AuthKey != "" {
		return a.AuthKey
	}
	return a.Key
}

// Login authenticates with email + password. On success the returned authKey is
// stored on the client and the user is returned. The password is not retained.
func (c *Client) Login(ctx context.Context, email, password string) (User, error) {
	var res authResult
	err := c.request(ctx, "login", map[string]any{
		"email":    email,
		"password": password,
		"facebook": false,
	}, &res)
	if err != nil {
		return User{}, err
	}
	if res.token() == "" {
		return User{}, fmt.Errorf("login: no auth key in response")
	}
	c.SetAuthKey(res.token())
	return res.User, nil
}

// GetUser validates the current authKey by fetching the account user.
func (c *Client) GetUser(ctx context.Context) (User, error) {
	if !c.LoggedIn() {
		return User{}, fmt.Errorf("not logged in")
	}
	var res struct {
		User User `json:"user"`
	}
	// Stremio returns the user either directly or under {"user":...}; try both.
	var raw json.RawMessage
	if err := c.request(ctx, "getUser", map[string]any{"authKey": c.AuthKey()}, &raw); err != nil {
		return User{}, err
	}
	if err := json.Unmarshal(raw, &res); err == nil && res.User.ID != "" {
		return res.User, nil
	}
	var u User
	if err := json.Unmarshal(raw, &u); err != nil {
		return User{}, fmt.Errorf("getUser: %w", err)
	}
	return u, nil
}

// Logout best-effort revokes the session server-side, then clears the local key.
// A network failure on the server call does not prevent the local clear.
func (c *Client) Logout(ctx context.Context) error {
	var serverErr error
	if c.LoggedIn() {
		serverErr = c.request(ctx, "logout", map[string]any{"authKey": c.AuthKey()}, nil)
	}
	c.SetAuthKey("")
	return serverErr
}

// Addon is one entry of the user's addon collection.
type Addon struct {
	TransportURL string          `json:"transportUrl"`
	Manifest     json.RawMessage `json:"manifest,omitempty"`
	Flags        json.RawMessage `json:"flags,omitempty"`
}

// AddonCollection fetches the user's synced addon collection.
func (c *Client) AddonCollection(ctx context.Context) ([]Addon, error) {
	if !c.LoggedIn() {
		return nil, fmt.Errorf("not logged in")
	}
	var res struct {
		Addons []Addon `json:"addons"`
	}
	err := c.request(ctx, "addonCollectionGet", map[string]any{
		"authKey": c.AuthKey(),
		"update":  true,
	}, &res)
	if err != nil {
		return nil, err
	}
	return res.Addons, nil
}

// LibraryState is the watched-progress state of a library item.
type LibraryState struct {
	LastWatched    string  `json:"lastWatched"`
	TimeOffset     float64 `json:"timeOffset"`
	Duration       float64 `json:"duration"`
	VideoID        string  `json:"video_id"` // e.g. "tt1234567:1:2" (series) or "" (movie)
	FlaggedWatched int     `json:"flaggedWatched"`
}

// LibraryItem is the subset of a Stremio libraryItem cremio maps to history.
type LibraryItem struct {
	ID      string       `json:"_id"`  // meta id, e.g. "tt1234567"
	Type    string       `json:"type"` // "movie" | "series"
	Name    string       `json:"name"`
	Removed bool         `json:"removed"`
	State   LibraryState `json:"state"`
	MTime   string       `json:"_mtime"`
}

// Library fetches all library items from the user's datastore.
func (c *Client) Library(ctx context.Context) ([]LibraryItem, error) {
	if !c.LoggedIn() {
		return nil, fmt.Errorf("not logged in")
	}
	var items []LibraryItem
	err := c.request(ctx, "datastoreGet", map[string]any{
		"authKey":    c.AuthKey(),
		"collection": "libraryItem",
		"all":        true,
		"ids":        []string{},
	}, &items)
	if err != nil {
		return nil, err
	}
	return items, nil
}
