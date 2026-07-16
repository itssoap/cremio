package account

// AddonURLs returns the transportUrl of each addon, skipping empties.
func AddonURLs(addons []Addon) []string {
	urls := make([]string, 0, len(addons))
	for _, a := range addons {
		if a.TransportURL != "" {
			urls = append(urls, a.TransportURL)
		}
	}
	return urls
}

// MergeAddonURLs unions incoming into existing: existing order is preserved
// first, then new unique incoming URLs are appended. It never removes an addon
// the user already has locally (server-side removals do not auto-propagate).
// Returns the merged slice and the number of URLs added.
func MergeAddonURLs(existing, incoming []string) (merged []string, added int) {
	seen := make(map[string]bool, len(existing))
	merged = make([]string, 0, len(existing)+len(incoming))
	for _, u := range existing {
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		merged = append(merged, u)
	}
	for _, u := range incoming {
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		merged = append(merged, u)
		added++
	}
	return merged, added
}
