package account

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/itssoap/cremio/internal/history"
)

// fakeServer returns an httptest server that replies with the given result for
// any /api/<method> POST, or an error envelope when errMsg is set.
func fakeServer(t *testing.T, handler func(method string, body map[string]any) (any, *apiError)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/api/")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		result, apiErr := handler(method, body)
		env := map[string]any{}
		if apiErr != nil {
			env["error"] = apiErr
		} else {
			env["result"] = result
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(env)
	}))
}

func TestLoginParsesAuthKeyAndUser(t *testing.T) {
	srv := fakeServer(t, func(method string, body map[string]any) (any, *apiError) {
		if method != "login" {
			t.Fatalf("unexpected method %q", method)
		}
		if body["password"] != "secret" || body["email"] != "a@b.com" {
			t.Fatalf("bad login body: %v", body)
		}
		return map[string]any{
			"authKey": "KEY123",
			"user":    map[string]any{"_id": "u1", "email": "a@b.com"},
		}, nil
	})
	defer srv.Close()

	c := newWithEndpoint(srv.URL)
	u, err := c.Login(context.Background(), "a@b.com", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthKey() != "KEY123" || !c.LoggedIn() {
		t.Fatalf("auth key not set: %q", c.AuthKey())
	}
	if u.Email != "a@b.com" || u.ID != "u1" {
		t.Fatalf("bad user: %+v", u)
	}
}

func TestErrorEnvelopeBecomesError(t *testing.T) {
	srv := fakeServer(t, func(method string, body map[string]any) (any, *apiError) {
		return nil, &apiError{Code: 3, Message: "wrong password"}
	})
	defer srv.Close()

	c := newWithEndpoint(srv.URL)
	_, err := c.Login(context.Background(), "a@b.com", "x")
	if err == nil || !strings.Contains(err.Error(), "wrong password") {
		t.Fatalf("expected error with message, got %v", err)
	}
}

func TestAddonCollectionParses(t *testing.T) {
	srv := fakeServer(t, func(method string, body map[string]any) (any, *apiError) {
		if body["authKey"] != "KEY" {
			t.Fatalf("missing authKey")
		}
		return map[string]any{
			"addons": []any{
				map[string]any{"transportUrl": "https://a/manifest.json"},
				map[string]any{"transportUrl": "https://b/manifest.json"},
			},
		}, nil
	})
	defer srv.Close()

	c := newWithEndpoint(srv.URL)
	c.SetAuthKey("KEY")
	addons, err := c.AddonCollection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	urls := AddonURLs(addons)
	if len(urls) != 2 || urls[0] != "https://a/manifest.json" {
		t.Fatalf("bad addons: %v", urls)
	}
}

func TestLibraryParses(t *testing.T) {
	srv := fakeServer(t, func(method string, body map[string]any) (any, *apiError) {
		return []any{
			map[string]any{
				"_id":  "tt111",
				"type": "movie",
				"state": map[string]any{
					"flaggedWatched": 1,
				},
			},
		}, nil
	})
	defer srv.Close()

	c := newWithEndpoint(srv.URL)
	c.SetAuthKey("KEY")
	items, err := c.Library(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "tt111" {
		t.Fatalf("bad library: %+v", items)
	}
}

func TestMergeAddonURLs(t *testing.T) {
	merged, added := MergeAddonURLs(
		[]string{"a", "b"},
		[]string{"b", "c", "c", ""},
	)
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	want := []string{"a", "b", "c"}
	if strings.Join(merged, ",") != strings.Join(want, ",") {
		t.Fatalf("merged = %v, want %v", merged, want)
	}
}

func TestApplyLibraryToHistory(t *testing.T) {
	h := &history.WatchHistory{}
	items := []LibraryItem{
		{ID: "tt1", Type: "movie", State: LibraryState{FlaggedWatched: 1}},
		{ID: "tt2", Type: "movie", State: LibraryState{Duration: 100, TimeOffset: 90}}, // 90% -> watched
		{ID: "tt3", Type: "movie", State: LibraryState{Duration: 100, TimeOffset: 10}}, // 10% -> not
		{ID: "tt4", Type: "series", State: LibraryState{VideoID: "tt4:1:2", FlaggedWatched: 1}},
		{ID: "tt5", Type: "movie", Removed: true, State: LibraryState{FlaggedWatched: 1}}, // removed -> skip
	}
	added := ApplyLibraryToHistory(h, items)
	if added != 3 {
		t.Fatalf("added = %d, want 3", added)
	}
	if !h.IsMovieWatched("tt1") || !h.IsMovieWatched("tt2") || h.IsMovieWatched("tt3") {
		t.Fatalf("movie marks wrong")
	}
	if !h.IsEpisodeWatched("tt4", 1, 2) {
		t.Fatalf("episode not marked")
	}
	// Idempotent: second apply adds nothing.
	if again := ApplyLibraryToHistory(h, items); again != 0 {
		t.Fatalf("second apply added %d, want 0", again)
	}
}
