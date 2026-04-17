// Package qobuz is a keyless scraper for qobuz.com's public search page. It
// returns the first album URL whose slug contains the queried artist, so a
// digest link lands on that band's actual release (subscribers play directly;
// non-subscribers get a Qobuz landing page).
//
// Qobuz has no stable unauthenticated JSON API, so this is HTML scraping.
// Kept narrow: one search URL, one regex for album links, one guard that
// rejects Qobuz's fuzzy "here's a different album entirely" fallback.
package qobuz

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const DefaultTimeout = 8 * time.Second

// ErrNoResult is returned when the search page has no album match for the
// requested artist. Cacheable — the caller stores "" so we don't re-query.
var ErrNoResult = errors.New("qobuz: no matching album")

type Client struct {
	http *http.Client
	ua   string
}

func New(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		http: &http.Client{Timeout: timeout},
		// Qobuz 403s most non-browser UAs on their search page, so we pose
		// as a generic desktop Chrome.
		ua: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36",
	}
}

// QueryKey returns the normalized cache key for an artist+title pair —
// mirrors lastfm.ArtistKey's style so cache keys read consistently.
func QueryKey(artist, title string) string {
	q := strings.TrimSpace(artist) + " " + strings.TrimSpace(title)
	return strings.Join(strings.Fields(strings.ToLower(q)), " ")
}

// albumLinkRe matches any `/us-en/album/<slug>/<id>` path from anchor tags
// on the search page. We capture the full relative path so the artist-guard
// can inspect the slug.
var albumLinkRe = regexp.MustCompile(`"(/us-en/album/[a-z0-9-]+/[a-z0-9]+)"`)

// SearchFirstAlbum returns the canonical Qobuz album URL for the first
// search hit whose slug contains the supplied artist. Retries on 5xx + 429.
// Returns ErrNoResult if the top N candidates all fail the artist check.
func (c *Client) SearchFirstAlbum(ctx context.Context, artist, title string) (string, error) {
	artist = strings.TrimSpace(artist)
	if artist == "" {
		return "", errors.New("qobuz: empty artist")
	}
	q := strings.TrimSpace(artist + " " + title)
	endpoint := "https://www.qobuz.com/us-en/search?q=" + url.QueryEscape(q) + "&go=tous"

	backoffs := []time.Duration{0, 400 * time.Millisecond, 1200 * time.Millisecond}
	var lastErr error
	for _, delay := range backoffs {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		out, err, retry := c.fetchSearch(ctx, endpoint, artist)
		if !retry {
			return out, err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("qobuz: all retries exhausted")
	}
	return "", lastErr
}

func (c *Client) fetchSearch(ctx context.Context, endpoint, artist string) (string, error, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err, false
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err, true
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode >= 500 && resp.StatusCode < 600:
		return "", fmt.Errorf("qobuz: transient HTTP %d", resp.StatusCode), true
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", fmt.Errorf("qobuz: rate-limited (HTTP 429)"), true
	default:
		return "", fmt.Errorf("qobuz: HTTP %d", resp.StatusCode), false
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", fmt.Errorf("qobuz: read body: %w", err), true
	}
	best := pickFirstMatchingAlbum(string(body), artist)
	if best == "" {
		return "", ErrNoResult, false
	}
	return "https://www.qobuz.com" + best, nil, false
}

// pickFirstMatchingAlbum walks the search HTML's album links in order and
// returns the first one whose slug contains the artist's normalized form.
// Rejects Qobuz's fuzzy "here's an unrelated album" fallbacks.
func pickFirstMatchingAlbum(html, artist string) string {
	wanted := normArtistSlug(artist)
	if wanted == "" {
		return ""
	}
	seen := make(map[string]struct{})
	for _, m := range albumLinkRe.FindAllStringSubmatch(html, -1) {
		path := m[1]
		if _, dup := seen[path]; dup {
			continue
		}
		seen[path] = struct{}{}
		if slugContainsArtist(path, wanted) {
			return path
		}
	}
	return ""
}

// normArtistSlug converts "Electric Callboy" → "electric-callboy" matching
// how Qobuz renders the artist component of its album slugs.
func normArtistSlug(artist string) string {
	s := strings.ToLower(strings.TrimSpace(artist))
	// Replace all non-alphanumeric runs with a single hyphen.
	var b strings.Builder
	prevHyphen := true // suppress leading hyphen
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// slugContainsArtist guards against Qobuz's fuzzy fallback matches. Qobuz
// slug shape is `<album-slug>-<artist-slug>`; the artist component is always
// at the tail, so a trailing substring match is the most discriminating
// check.
func slugContainsArtist(qobuzPath, artistSlug string) bool {
	// qobuzPath like "/us-en/album/tekkno-electric-callboy/njbfh0gfpsaub"
	parts := strings.Split(strings.TrimPrefix(qobuzPath, "/us-en/album/"), "/")
	if len(parts) < 1 {
		return false
	}
	slug := parts[0]
	// Accept either suffix-match ("…-electric-callboy") or a hyphen-bounded
	// substring ("electric-callboy-live"). Bare substring would over-match
	// ("ca" hitting "carousel"), so we require hyphen boundaries.
	if slug == artistSlug {
		return true
	}
	if strings.HasSuffix(slug, "-"+artistSlug) {
		return true
	}
	if strings.HasPrefix(slug, artistSlug+"-") {
		return true
	}
	if strings.Contains(slug, "-"+artistSlug+"-") {
		return true
	}
	return false
}
