package tui

import "strings"

// Filter holds parsed include and exclude keywords.
type Filter struct {
	includes []string
	excludes []string
}

// ParseFilter parses a filter string like "+hello -bye world" into include/exclude keywords.
// "+" prefix or no prefix means include, "-" prefix means exclude.
func ParseFilter(input string) Filter {
	var f Filter
	for _, token := range strings.Fields(input) {
		if strings.HasPrefix(token, "-") {
			kw := strings.ToLower(strings.TrimPrefix(token, "-"))
			if kw != "" {
				f.excludes = append(f.excludes, kw)
			}
		} else {
			kw := strings.ToLower(strings.TrimPrefix(token, "+"))
			if kw != "" {
				f.includes = append(f.includes, kw)
			}
		}
	}
	return f
}

// IsEmpty returns true if no filter keywords are set.
func (f Filter) IsEmpty() bool {
	return len(f.includes) == 0 && len(f.excludes) == 0
}

// Match returns true if the combined text of the given strings satisfies the
// filter. Callers pass every field shown to the user (name, title, description)
// so the filter matches against the same text the user reads on screen.
// All include keywords must appear, and no exclude keywords may appear.
func (f Filter) Match(texts ...string) bool {
	combined := strings.ToLower(strings.Join(texts, " "))
	for _, kw := range f.excludes {
		if strings.Contains(combined, kw) {
			return false
		}
	}
	for _, kw := range f.includes {
		if !strings.Contains(combined, kw) {
			return false
		}
	}
	return true
}
