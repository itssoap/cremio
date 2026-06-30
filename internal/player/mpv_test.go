package player

import "testing"

func TestValidatePlayable(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"empty", "", true},
		{"option injection long", "--script=/tmp/evil.lua", true},
		{"option injection short", "-x", true},
		{"http", "http://example.com/a.mkv", false},
		{"https", "https://example.com/a.mkv", false},
		{"magnet", "magnet:?xt=urn:btih:abc", false},
		{"rtmp", "rtmp://example.com/live", false},
		{"rtmps", "rtmps://example.com/live", false},
		{"unsupported scheme ftp", "ftp://example.com/a", true},
		{"unsupported scheme file", "file:///etc/passwd", true},
		{"no scheme", "example.com/a.mkv", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePlayable(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePlayable(%q) err = %v, wantErr = %v", tt.url, err, tt.wantErr)
			}
		})
	}
}
