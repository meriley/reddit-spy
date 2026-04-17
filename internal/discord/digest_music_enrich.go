package discord

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/go-kit/log/level"

	ctxpkg "github.com/meriley/reddit-spy/internal/context"
	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	"github.com/meriley/reddit-spy/internal/lastfm"
	"github.com/meriley/reddit-spy/internal/llm"
)

const (
	// lastfmCacheTTL — refetch a cached artist after this long.
	lastfmCacheTTL = 30 * 24 * time.Hour
	// lastfmConcurrency — max parallel Last.fm lookups per enrichment pass.
	// Kept deliberately low; Last.fm's front-end returns 502s under burst
	// load. 2 is polite, still fast enough to enrich 40-80 entries inside
	// the overall budget.
	lastfmConcurrency = 2
	// lastfmPerRequestTimeout — hard cap on a single Last.fm fetch including
	// the internal retry-on-502 backoff (2 retries × 0.9s + request time).
	lastfmPerRequestTimeout = 8 * time.Second
	// lastfmTotalBudget — cap the whole enrichment pass so a thread with
	// 200 artists can't stall a Discord interaction. Entries that don't
	// complete in time keep Listeners=0.
	lastfmTotalBudget = 45 * time.Second
)

// enrichMusicListeners fills MusicEntry.Listeners for each entry using the
// store's Last.fm cache, falling back to the scraper for misses and stale
// rows. Returns a new slice; never mutates the input. On any error per
// artist it logs a warning and leaves that entry's Listeners at 0.
func (c *Client) enrichMusicListeners(ctx ctxpkg.Ctx, entries []llm.MusicEntry) []llm.MusicEntry {
	if c.lastfm == nil || len(entries) == 0 {
		return entries
	}

	out := make([]llm.MusicEntry, len(entries))
	copy(out, entries)

	budgetCtx, cancel := context.WithTimeout(ctx, lastfmTotalBudget)
	defer cancel()

	sem := make(chan struct{}, lastfmConcurrency)
	var wg sync.WaitGroup

	for i := range out {
		if out[i].Artist == "" {
			continue
		}
		// If the input already has a non-zero Listeners (e.g. from a prior
		// day's enrichment that we merged into the rolling_posts row), skip
		// — no reason to refetch.
		if out[i].Listeners > 0 {
			continue
		}
		wg.Add(1)
		i := i
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			key := lastfm.ArtistKey(out[i].Artist)

			// Cache lookup first (new tags-aware call).
			listeners, tags, fetchedAt, ok, err := c.Bot.Store.GetLastfmArtist(budgetCtx, key)
			if err != nil {
				_ = level.Warn(ctx.Log()).Log("msg", "lastfm cache lookup failed", "artist", out[i].Artist, "error", err)
			}
			if ok && time.Since(fetchedAt) < lastfmCacheTTL {
				out[i].Listeners = listeners
				out[i].Tags = tags
				return
			}

			// Miss or stale — hit Last.fm for listeners + tags in one fetch.
			reqCtx, reqCancel := context.WithTimeout(budgetCtx, lastfmPerRequestTimeout)
			defer reqCancel()
			info, ferr := c.lastfm.LookupArtist(reqCtx, out[i].Artist)
			if ferr != nil {
				if errors.Is(ferr, lastfm.ErrNotFound) {
					// Cacheable zero — artist genuinely not on Last.fm.
					if werr := c.Bot.Store.UpsertLastfmArtist(budgetCtx, key, 0, nil); werr != nil {
						_ = level.Warn(ctx.Log()).Log("msg", "lastfm cache upsert (not-found) failed", "artist", out[i].Artist, "error", werr)
					}
					return
				}
				_ = level.Warn(ctx.Log()).Log("msg", "lastfm lookup failed", "artist", out[i].Artist, "error", ferr)
				return
			}
			out[i].Listeners = info.Listeners
			out[i].Tags = info.Tags
			if werr := c.Bot.Store.UpsertLastfmArtist(budgetCtx, key, info.Listeners, info.Tags); werr != nil {
				_ = level.Warn(ctx.Log()).Log("msg", "lastfm cache upsert failed", "artist", out[i].Artist, "error", werr)
			}
		}()
	}
	wg.Wait()
	return out
}

// mergeListeners carries previously-enriched listener counts and tags from
// prior entries into fresh, matched by dedupe key. Lets a same-day edit skip
// re-looking-up artists we already enriched on the first match.
func mergeListeners(fresh, prior []llm.MusicEntry) []llm.MusicEntry {
	if len(prior) == 0 {
		return fresh
	}
	type entry struct {
		listeners int
		tags      []string
	}
	byKey := make(map[string]entry, len(prior))
	for _, p := range prior {
		if p.Listeners == 0 && len(p.Tags) == 0 {
			continue
		}
		byKey[llm.MusicDedupeKey(p)] = entry{listeners: p.Listeners, tags: p.Tags}
	}
	for i := range fresh {
		e, ok := byKey[llm.MusicDedupeKey(fresh[i])]
		if !ok {
			continue
		}
		if fresh[i].Listeners == 0 {
			fresh[i].Listeners = e.listeners
		}
		if len(fresh[i].Tags) == 0 {
			fresh[i].Tags = e.tags
		}
	}
	return fresh
}

// Compile-time assertion that the store's Last.fm cache methods are visible
// on the Store interface (so downstream test mocks fail the build early).
var _ dbstore.Store = (*dbstore.PGXStore)(nil)
