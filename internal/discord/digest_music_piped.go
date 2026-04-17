package discord

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/go-kit/log/level"

	ctxpkg "github.com/meriley/reddit-spy/internal/context"
	"github.com/meriley/reddit-spy/internal/llm"
	"github.com/meriley/reddit-spy/internal/piped"
)

const (
	pipedCacheTTL          = 30 * 24 * time.Hour
	pipedConcurrency       = 2
	pipedPerRequestTimeout = 7 * time.Second
	pipedTotalBudget       = 60 * time.Second
)

// enrichMusicYouTubeIDs fills MusicEntry.YoutubeURL. Strategy:
//   - album/ep kind → try music_albums first (playlist URL); fall back to
//     music_songs (one of the singles off the release) if no album hit.
//   - single kind → music_songs directly.
//
// Cached results are keyed per-filter so album and song searches of the
// same "artist title" never collide.
func (c *Client) enrichMusicYouTubeIDs(ctx ctxpkg.Ctx, entries []llm.MusicEntry) []llm.MusicEntry {
	if c.piped == nil || len(entries) == 0 {
		return entries
	}
	out := make([]llm.MusicEntry, len(entries))
	copy(out, entries)

	budgetCtx, cancel := context.WithTimeout(ctx, pipedTotalBudget)
	defer cancel()

	sem := make(chan struct{}, pipedConcurrency)
	var wg sync.WaitGroup

	for i := range out {
		if out[i].Artist == "" || out[i].Title == "" {
			continue
		}
		if out[i].YoutubeURL != "" {
			continue
		}
		wg.Add(1)
		i := i
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out[i].YoutubeURL = c.resolveYouTubeURL(budgetCtx, ctx, out[i])
		}()
	}
	wg.Wait()
	return out
}

// resolveYouTubeURL runs the filter sequence appropriate for e.Kind, hitting
// the cache first for each filter before going to Piped.
func (c *Client) resolveYouTubeURL(budgetCtx context.Context, logCtx ctxpkg.Ctx, e llm.MusicEntry) string {
	filters := filterOrderFor(e.Kind)
	for _, f := range filters {
		if url := c.lookupPipedFilter(budgetCtx, logCtx, f, e.Artist, e.Title); url != "" {
			return url
		}
	}
	return ""
}

// filterOrderFor returns the ordered list of Piped filters to try for a given
// entry kind. Album/EP entries prefer the album playlist when it exists, then
// fall through to a song from the release. Singles go straight to songs.
func filterOrderFor(kind string) []string {
	switch strings.ToLower(kind) {
	case "album", "ep":
		return []string{piped.FilterMusicAlbums, piped.FilterMusicSongs}
	default:
		return []string{piped.FilterMusicSongs}
	}
}

// lookupPipedFilter tries one filter: cache → Piped → cache-write. Returns
// "" when either the cache is negative-hit ("") or Piped returned nothing;
// in both cases the caller falls through to the next filter.
func (c *Client) lookupPipedFilter(budgetCtx context.Context, logCtx ctxpkg.Ctx, filter, artist, title string) string {
	key := piped.CacheKey(filter, artist, title)

	cachedURL, fetchedAt, ok, err := c.Bot.Store.GetPipedVideo(budgetCtx, key)
	if err != nil {
		_ = level.Warn(logCtx.Log()).Log("msg", "piped cache lookup failed", "query", key, "error", err)
	}
	if ok && time.Since(fetchedAt) < pipedCacheTTL {
		return cachedURL // may be "" — legitimate cached "no match"
	}

	reqCtx, reqCancel := context.WithTimeout(budgetCtx, pipedPerRequestTimeout)
	defer reqCancel()

	raw, perr := c.piped.Search(reqCtx, piped.QueryKey(artist, title), filter)
	if perr != nil {
		if errors.Is(perr, piped.ErrNoResult) {
			// Cacheable no-match.
			if werr := c.Bot.Store.UpsertPipedVideo(budgetCtx, key, ""); werr != nil {
				_ = level.Warn(logCtx.Log()).Log("msg", "piped cache upsert (empty) failed", "query", key, "error", werr)
			}
			return ""
		}
		_ = level.Warn(logCtx.Log()).Log("msg", "piped search failed", "query", key, "error", perr)
		return ""
	}
	url := piped.ToYouTubeURL(raw)
	if werr := c.Bot.Store.UpsertPipedVideo(budgetCtx, key, url); werr != nil {
		_ = level.Warn(logCtx.Log()).Log("msg", "piped cache upsert failed", "query", key, "error", werr)
	}
	return url
}
