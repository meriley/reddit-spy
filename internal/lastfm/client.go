// Package lastfm is a tiny, key-less scraper for Last.fm's public artist page.
// It pulls the listener count used to rank music-digest entries by popularity.
// No API key, no auth; the scraper is intentionally narrow so there's little
// surface for Last.fm's HTML to drift out from under us. On any failure the
// caller should treat the result as "unknown" (0) rather than bubbling the
// error — popularity is a nice-to-have, not load-bearing.
package lastfm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const DefaultTimeout = 5 * time.Second

// ErrNotFound is returned when Last.fm has no page for the supplied artist.
var ErrNotFound = errors.New("lastfm: artist not found")

type Client struct {
	http *http.Client
	ua   string
}

// New builds a Client with a sensible default timeout and a User-Agent that
// identifies the bot, following Last.fm's courtesy convention for scrapers.
func New(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		http: &http.Client{Timeout: timeout},
		ua:   "reddit-spy/music-digest (+gitea.cmtriley.com/mriley/reddit-spy)",
	}
}

// ArtistKey returns the normalized cache key for a raw artist string.
// Case-folded, single-spaced, trimmed. Callers use this both as the cache
// primary key and to derive the Last.fm URL slug.
func ArtistKey(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	return strings.Join(strings.Fields(s), " ")
}

// LookupListeners fetches the listener count for a single artist. Returns
// ErrNotFound on HTTP 404. Any other failure (network, unexpected HTML shape,
// context deadline) returns a non-nil error; callers should not cache those
// outcomes as a zero-listener hit.
func (c *Client) LookupListeners(ctx context.Context, artist string) (int, error) {
	key := ArtistKey(artist)
	if key == "" {
		return 0, errors.New("lastfm: empty artist")
	}
	// Last.fm's artist URL encodes spaces as '+', not %20, and leaves most
	// punctuation alone. url.QueryEscape gets us close enough; the ' ' → '+'
	// is the important bit for actual artist slugs.
	slug := strings.ReplaceAll(url.QueryEscape(artist), "%20", "+")
	endpoint := "https://www.last.fm/music/" + slug

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return 0, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("lastfm: HTTP %d for %q", resp.StatusCode, endpoint)
	}

	// Cap body reads at 1 MiB — artist pages are ~300-500 KB in practice; a
	// runaway would otherwise make a single lookup dominate the poll tick.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, fmt.Errorf("lastfm: read body: %w", err)
	}

	n, err := extractListeners(string(body))
	if err != nil {
		return 0, err
	}
	return n, nil
}

// extractListeners finds the listener count in a Last.fm artist page. The
// page carries the canonical count in an <abbr title="5,323,765"> tag next to
// the "Listeners" label — we match against a small set of resilient patterns
// so a CSS-class rename doesn't take the whole digest with it.
var listenerPatterns = []*regexp.Regexp{
	// Preferred: the exact-count tooltip next to the Listeners header.
	// <abbr title="5,323,765" class="intro-stats-number">5.3M</abbr>
	regexp.MustCompile(`<abbr[^>]*title="([0-9,]+)"[^>]*class="[^"]*intro-stats-number`),
	// JSON-LD variant (Last.fm has emitted this on some pages).
	// "interactionStatistic":{"@type":"InteractionCounter","userInteractionCount":5323765
	regexp.MustCompile(`"userInteractionCount"\s*:\s*"?([0-9,]+)"?`),
	// Fallback: `>5,323,765</abbr>\s*listeners` form (case-insensitive).
	regexp.MustCompile(`(?i)>\s*([0-9,]+)\s*</[a-z]+>\s*listeners`),
}

func extractListeners(html string) (int, error) {
	for _, re := range listenerPatterns {
		m := re.FindStringSubmatch(html)
		if m == nil {
			continue
		}
		clean := strings.ReplaceAll(m[1], ",", "")
		n, err := strconv.Atoi(clean)
		if err != nil {
			continue
		}
		return n, nil
	}
	return 0, errors.New("lastfm: listener count not found on page (HTML shape changed?)")
}
