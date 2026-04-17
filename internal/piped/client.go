// Package piped is a narrow client for the Piped API
// (https://github.com/TeamPiped/Piped) used to attach a YouTube link to each
// music-digest entry so Discord renders it as clickable. Keyless: any public
// or self-hosted instance works behind PIPED_BASE_URL.
package piped

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultTimeout = 6 * time.Second

// ErrNoResult is returned when a Piped search returns zero items.
var ErrNoResult = errors.New("piped: no search result")

// Known Piped search filters used by this client.
const (
	FilterMusicSongs  = "music_songs"
	FilterMusicAlbums = "music_albums"
)

type Client struct {
	http    *http.Client
	baseURL string
	ua      string
}

// New returns a Client pointed at the supplied Piped API base URL (e.g.
// "https://api.piped.private.coffee" or an in-cluster service DNS).
func New(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		http:    &http.Client{Timeout: timeout},
		baseURL: strings.TrimRight(baseURL, "/"),
		ua:      "reddit-spy/music-digest (+gitea.cmtriley.com/mriley/reddit-spy)",
	}
}

// QueryKey returns the normalized query string for an artist+title pair.
// Used as the search term AND as part of the cache key (prefixed by the
// search filter).
func QueryKey(artist, title string) string {
	q := strings.TrimSpace(artist) + " " + strings.TrimSpace(title)
	q = strings.Join(strings.Fields(strings.ToLower(q)), " ")
	return q
}

// CacheKey combines a Piped filter with a query, yielding a stable primary
// key for the on-disk Piped result cache.
func CacheKey(filter string, artist, title string) string {
	return filter + "|" + QueryKey(artist, title)
}

// Search returns the first Piped hit's relative URL (e.g. "/watch?v=abcd" or
// "/playlist?list=OLAK…") for the supplied query under the given filter.
// Retries on 5xx + 429 with short backoff. ErrNoResult is a cacheable empty
// hit; the caller should store it to avoid re-querying.
func (c *Client) Search(ctx context.Context, query, filter string) (string, error) {
	if c.baseURL == "" {
		return "", errors.New("piped: empty base URL")
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return "", errors.New("piped: empty query")
	}
	endpoint := fmt.Sprintf("%s/search?filter=%s&q=%s", c.baseURL, url.QueryEscape(filter), url.QueryEscape(q))

	backoffs := []time.Duration{0, 250 * time.Millisecond, 750 * time.Millisecond}
	var lastErr error
	for _, delay := range backoffs {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		u, err, retry := c.fetch(ctx, endpoint, filter)
		if !retry {
			return u, err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("piped: all retries exhausted")
	}
	return "", lastErr
}

// SearchFirstSong is a legacy wrapper — `Search(ctx, QueryKey(a,t), FilterMusicSongs)`.
// Kept so callers that only want a song (singles mode) stay terse.
func (c *Client) SearchFirstSong(ctx context.Context, artist, title string) (string, error) {
	raw, err := c.Search(ctx, QueryKey(artist, title), FilterMusicSongs)
	if err != nil {
		return "", err
	}
	return videoIDFromURL(raw), nil
}

// fetch performs a single HTTP attempt. The retry flag tells the caller if
// it's safe to replay.
func (c *Client) fetch(ctx context.Context, endpoint, filter string) (string, error, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err, false
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err, true
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode >= 500 && resp.StatusCode < 600:
		return "", fmt.Errorf("piped: transient HTTP %d", resp.StatusCode), true
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", fmt.Errorf("piped: rate-limited (HTTP 429)"), true
	default:
		return "", fmt.Errorf("piped: HTTP %d", resp.StatusCode), false
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("piped: read body: %w", err), true
	}
	var out struct {
		Items []struct {
			URL  string `json:"url"`
			Type string `json:"type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("piped: decode response: %w", err), false
	}

	want := firstItemTypeFor(filter)
	for _, it := range out.Items {
		if want != "" && it.Type != "" && it.Type != want {
			continue
		}
		if it.URL != "" {
			return it.URL, nil, false
		}
	}
	return "", ErrNoResult, false
}

// firstItemTypeFor returns the expected Piped result type for a given filter.
// music_albums → "playlist" (album playlists); music_songs → "stream".
func firstItemTypeFor(filter string) string {
	switch filter {
	case FilterMusicAlbums:
		return "playlist"
	case FilterMusicSongs:
		return "stream"
	}
	return ""
}

// ToYouTubeURL maps a Piped-relative URL to a music.youtube.com URL the
// digest renders. Returns "" for anything it doesn't know how to handle.
func ToYouTubeURL(relative string) string {
	if relative == "" {
		return ""
	}
	u, err := url.Parse(relative)
	if err != nil {
		return ""
	}
	switch {
	case u.Path == "/watch" && u.Query().Get("v") != "":
		return "https://music.youtube.com/watch?v=" + u.Query().Get("v")
	case u.Path == "/playlist" && u.Query().Get("list") != "":
		return "https://music.youtube.com/playlist?list=" + u.Query().Get("list")
	case strings.HasSuffix(u.Host, "youtu.be") && strings.Trim(u.Path, "/") != "":
		return "https://music.youtube.com/watch?v=" + strings.Split(strings.Trim(u.Path, "/"), "/")[0]
	}
	return ""
}

// videoIDFromURL is retained for the legacy SearchFirstSong path + tests.
func videoIDFromURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if v := u.Query().Get("v"); v != "" {
		return v
	}
	if strings.HasSuffix(u.Host, "youtu.be") {
		if p := strings.Trim(u.Path, "/"); p != "" {
			return strings.Split(p, "/")[0]
		}
	}
	return ""
}

// YoutubeURL is the legacy id-only formatter. New callers should use
// ToYouTubeURL on the raw relative URL so playlist links survive.
func YoutubeURL(videoID string) string {
	if videoID == "" {
		return ""
	}
	return "https://music.youtube.com/watch?v=" + videoID
}
