// Package piped is a narrow client for the Piped API
// (https://github.com/TeamPiped/Piped) used to attach a YouTube videoId to
// each music-digest entry so Discord renders a clickable link. Keyless: any
// public or self-hosted instance works behind PIPED_BASE_URL.
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

type Client struct {
	http    *http.Client
	baseURL string
	ua      string
}

// New returns a Client pointed at the supplied Piped API base URL (e.g.
// "https://pipedapi.kavin.rocks" or the in-cluster service DNS). Trailing
// slashes are normalized away.
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

// QueryKey returns the normalized cache key for an artist+title pair. The
// bot uses it both as the DB primary key and as the search query string.
func QueryKey(artist, title string) string {
	q := strings.TrimSpace(artist) + " " + strings.TrimSpace(title)
	q = strings.Join(strings.Fields(strings.ToLower(q)), " ")
	return q
}

// SearchFirstSong returns the videoId of the first music-song hit for the
// `artist title` query. Retries on transient upstream failures (5xx) with
// short backoff, matching the Last.fm client's pattern. Returns ErrNoResult
// when the response has an empty items array — treat that as a cacheable
// "no video" outcome.
func (c *Client) SearchFirstSong(ctx context.Context, artist, title string) (string, error) {
	if c.baseURL == "" {
		return "", errors.New("piped: empty base URL")
	}
	q := QueryKey(artist, title)
	if q == "" {
		return "", errors.New("piped: empty query")
	}

	endpoint := fmt.Sprintf("%s/search?filter=music_songs&q=%s", c.baseURL, url.QueryEscape(q))
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
		id, err, retry := c.search(ctx, endpoint)
		if !retry {
			return id, err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("piped: all retries exhausted")
	}
	return "", lastErr
}

// search performs one HTTP attempt. The retry bool tells the caller whether
// it's safe to replay.
func (c *Client) search(ctx context.Context, endpoint string) (string, error, bool) {
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
		// fall through
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
	for _, it := range out.Items {
		// Piped marks song hits as "stream"; anything else (channel, playlist
		// etc.) is ignored — we only want playable video ids.
		if it.Type != "" && it.Type != "stream" {
			continue
		}
		if id := videoIDFromURL(it.URL); id != "" {
			return id, nil, false
		}
	}
	return "", ErrNoResult, false
}

// videoIDFromURL pulls `abcd1234` out of "/watch?v=abcd1234" or
// "https://www.youtube.com/watch?v=abcd1234&list=…" or "https://youtu.be/abcd".
// Returns "" for anything else.
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
	// youtu.be short form: host is "youtu.be", id is the first path segment.
	if strings.HasSuffix(u.Host, "youtu.be") {
		if p := strings.Trim(u.Path, "/"); p != "" {
			return strings.Split(p, "/")[0]
		}
	}
	return ""
}

// YoutubeURL formats a videoId as a music.youtube.com URL — opens in YT Music
// for users who prefer it, falls back to regular playback for everyone else.
func YoutubeURL(videoID string) string {
	if videoID == "" {
		return ""
	}
	return "https://music.youtube.com/watch?v=" + videoID
}
