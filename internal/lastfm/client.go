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

// ArtistInfo carries the subset of Last.fm artist-page signals the digest
// uses. Listeners is the Last.fm monthly-listener count. Tags is a short
// genre list taken from the page's "Related Tags" block (most-voted first,
// up to MaxTags entries).
type ArtistInfo struct {
	Listeners int
	Tags      []string
}

// MaxTags caps how many tags we store per artist.
const MaxTags = 3

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

// LookupListeners is a thin convenience wrapper around LookupArtist for
// callers that only care about the listener count.
func (c *Client) LookupListeners(ctx context.Context, artist string) (int, error) {
	info, err := c.LookupArtist(ctx, artist)
	if err != nil {
		return 0, err
	}
	return info.Listeners, nil
}

// LookupArtist fetches listener count + tags for a single artist. Returns
// ErrNotFound on HTTP 404. Retries on transient upstream failures (502/503/
// 504) with short exponential backoff — Last.fm throws 502s under moderate
// burst load, and a single retry usually recovers.
func (c *Client) LookupArtist(ctx context.Context, artist string) (ArtistInfo, error) {
	key := ArtistKey(artist)
	if key == "" {
		return ArtistInfo{}, errors.New("lastfm: empty artist")
	}
	slug := strings.ReplaceAll(url.QueryEscape(artist), "%20", "+")
	endpoint := "https://www.last.fm/music/" + slug

	backoffs := []time.Duration{0, 300 * time.Millisecond, 900 * time.Millisecond}
	var lastErr error
	for _, delay := range backoffs {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ArtistInfo{}, ctx.Err()
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return ArtistInfo{}, err
		}
		req.Header.Set("User-Agent", c.ua)
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusNotFound {
			_ = resp.Body.Close()
			return ArtistInfo{}, ErrNotFound
		}
		if resp.StatusCode == http.StatusBadGateway ||
			resp.StatusCode == http.StatusServiceUnavailable ||
			resp.StatusCode == http.StatusGatewayTimeout {
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("lastfm: transient HTTP %d for %q", resp.StatusCode, endpoint)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return ArtistInfo{}, fmt.Errorf("lastfm: HTTP %d for %q", resp.StatusCode, endpoint)
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("lastfm: read body: %w", err)
			continue
		}

		html := string(body)
		n, perr := extractListeners(html)
		if perr != nil {
			return ArtistInfo{}, perr
		}
		return ArtistInfo{Listeners: n, Tags: extractTags(html, MaxTags)}, nil
	}
	if lastErr == nil {
		lastErr = errors.New("lastfm: all retries exhausted")
	}
	return ArtistInfo{}, lastErr
}

// tagsBlockRe captures the outer <ul class="tags-list..."> block. The
// extractor then pulls each <a>tagname</a> out of it in display order
// (which is Last.fm's vote-weighted ordering).
var tagsBlockRe = regexp.MustCompile(`(?s)<ul\s+class="[^"]*tags-list[^"]*"\s*>(.*?)</ul>`)
var tagAnchorRe = regexp.MustCompile(`(?s)<a[^>]*href="/tag/[^"]+"[^>]*>\s*([^<]+?)\s*</a>`)

// extractTags returns up to maxTags lowercase tags from the artist page's
// "Related Tags" block. Returns nil (not an error) if no tags block is
// found — tags are advisory; the digest still works without them.
func extractTags(html string, maxTags int) []string {
	m := tagsBlockRe.FindStringSubmatch(html)
	if m == nil {
		return nil
	}
	block := m[1]
	anchors := tagAnchorRe.FindAllStringSubmatch(block, -1)
	out := make([]string, 0, maxTags)
	for _, a := range anchors {
		tag := strings.ToLower(strings.TrimSpace(a[1]))
		if tag == "" {
			continue
		}
		out = append(out, tag)
		if len(out) >= maxTags {
			break
		}
	}
	return out
}

// extractListeners finds the listener count in a Last.fm artist page. The
// page renders the canonical count in a `<abbr class="intabbr js-abbreviated-counter"
// title="305,227">305.2K</abbr>` element immediately after the `Listeners</h4>`
// header. We try a tight regex first (header + nearest abbr) and fall through
// to progressively fuzzier patterns so a CSS-class rename or HTML rearrange
// doesn't blind the scraper wholesale.
var listenerPatterns = []*regexp.Regexp{
	// Preferred: the abbr immediately following the "Listeners" section header.
	// (?s) = dot matches newlines; non-greedy `.*?` keeps the nearest match.
	regexp.MustCompile(`(?s)Listeners\s*</h4>.*?<abbr[^>]*title="([0-9,]+)"`),
	// Second cut: the intabbr stats element itself, first occurrence on the
	// page (listeners is always rendered before scrobbles in the header).
	regexp.MustCompile(`<abbr[^>]*class="[^"]*intabbr[^"]*"[^>]*title="([0-9,]+)"`),
	// Legacy layout: some older artist pages used intro-stats-number.
	regexp.MustCompile(`<abbr[^>]*title="([0-9,]+)"[^>]*class="[^"]*intro-stats-number`),
	// JSON-LD variant (Last.fm has emitted this on some pages).
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
