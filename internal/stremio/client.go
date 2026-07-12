package stremio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	http *http.Client
}

func NewClient() *Client {
	return &Client{
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func NormalizeBaseURL(rawURL string) string {
	rawURL = strings.TrimRight(rawURL, "/")
	rawURL = strings.TrimSuffix(rawURL, "/manifest.json")
	return rawURL
}

func (c *Client) FetchManifest(ctx context.Context, baseURL string) (*Manifest, error) {
	baseURL = NormalizeBaseURL(baseURL)
	u := baseURL + "/manifest.json"
	var m Manifest
	if err := c.getJSON(ctx, u, &m); err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	return &m, nil
}

func (c *Client) FetchCatalog(ctx context.Context, baseURL, contentType, catalogID string) (*CatalogResponse, error) {
	baseURL = NormalizeBaseURL(baseURL)
	u := fmt.Sprintf("%s/catalog/%s/%s.json", baseURL, url.PathEscape(contentType), url.PathEscape(catalogID))
	var resp CatalogResponse
	if err := c.getJSON(ctx, u, &resp); err != nil {
		return nil, fmt.Errorf("fetch catalog: %w", err)
	}
	return &resp, nil
}

func (c *Client) SearchCatalog(ctx context.Context, baseURL, contentType, catalogID, query string) (*CatalogResponse, error) {
	baseURL = NormalizeBaseURL(baseURL)
	u := fmt.Sprintf("%s/catalog/%s/%s/search=%s.json", baseURL, url.PathEscape(contentType), url.PathEscape(catalogID), url.PathEscape(query))
	var resp CatalogResponse
	if err := c.getJSON(ctx, u, &resp); err != nil {
		return nil, fmt.Errorf("search catalog: %w", err)
	}
	return &resp, nil
}

func (c *Client) FetchMeta(ctx context.Context, baseURL, contentType, id string) (*MetaResponse, error) {
	baseURL = NormalizeBaseURL(baseURL)
	u := fmt.Sprintf("%s/meta/%s/%s.json", baseURL, url.PathEscape(contentType), url.PathEscape(id))
	var resp MetaResponse
	if err := c.getJSON(ctx, u, &resp); err != nil {
		return nil, fmt.Errorf("fetch meta: %w", err)
	}
	return &resp, nil
}

func (c *Client) FetchStreams(ctx context.Context, baseURL, contentType, id string) (*StreamResponse, error) {
	baseURL = NormalizeBaseURL(baseURL)
	u := fmt.Sprintf("%s/stream/%s/%s.json", baseURL, url.PathEscape(contentType), url.PathEscape(id))
	var resp StreamResponse
	if err := c.getJSON(ctx, u, &resp); err != nil {
		return nil, fmt.Errorf("fetch streams: %w", err)
	}
	return &resp, nil
}

func (c *Client) getJSON(ctx context.Context, u string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	// Some addons (e.g. torrentio) sit behind Cloudflare, which rejects
	// Go's default User-Agent with HTTP 403.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, u)
	}

	return json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(target)
}
