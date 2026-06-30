package tui

import "testing"

func TestParseFilterAndMatch(t *testing.T) {
	f := ParseFilter("+1080p web -cam")
	if f.IsEmpty() {
		t.Fatal("expected non-empty filter")
	}
	if !f.Match("Movie 1080p WEB-DL") {
		t.Error("expected match for included keywords")
	}
	if f.Match("Movie 1080p CAM") {
		t.Error("expected no match when an exclude keyword is present")
	}
	if f.Match("Movie 720p web") {
		t.Error("expected no match when an include keyword is missing")
	}
}

func TestParseFilterEmpty(t *testing.T) {
	if !ParseFilter("").IsEmpty() {
		t.Error("empty input should produce an empty filter")
	}
	if !ParseFilter("   +  -  ").IsEmpty() {
		t.Error("bare prefixes should not create keywords")
	}
}

func TestFilterMatchCaseInsensitive(t *testing.T) {
	f := ParseFilter("HDR")
	if !f.Match("movie hdr10") {
		t.Error("matching should be case-insensitive")
	}
}

func TestFilterMatchAcrossFields(t *testing.T) {
	f := ParseFilter("5gb")
	if !f.Match("Some Addon", "1080p 5GB seeders:50") {
		t.Error("expected match across combined fields")
	}
}
