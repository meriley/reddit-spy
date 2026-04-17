package discord

import (
	"context"
	"errors"
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
	pipedTotalBudget       = 40 * time.Second
)

// enrichMusicYouTubeIDs fills MusicEntry.YoutubeID via the Piped cache +
// on-demand search. No-op when the Piped client is nil. Mirrors the Last.fm
// enrichment's failure-is-soft posture: per-entry errors are logged and the
// entry just loses its link, they don't abort the pass.
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
		if out[i].YoutubeID != "" {
			continue
		}
		wg.Add(1)
		i := i
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			key := piped.QueryKey(out[i].Artist, out[i].Title)

			// Cache hit (including the cached "no result" → empty video_id).
			videoID, fetchedAt, ok, err := c.Bot.Store.GetPipedVideo(budgetCtx, key)
			if err != nil {
				_ = level.Warn(ctx.Log()).Log("msg", "piped cache lookup failed", "query", key, "error", err)
			}
			if ok && time.Since(fetchedAt) < pipedCacheTTL {
				out[i].YoutubeID = videoID
				return
			}

			reqCtx, reqCancel := context.WithTimeout(budgetCtx, pipedPerRequestTimeout)
			defer reqCancel()
			id, perr := c.piped.SearchFirstSong(reqCtx, out[i].Artist, out[i].Title)
			if perr != nil {
				if errors.Is(perr, piped.ErrNoResult) {
					// Cacheable empty — nothing indexed for this query.
					if werr := c.Bot.Store.UpsertPipedVideo(budgetCtx, key, ""); werr != nil {
						_ = level.Warn(ctx.Log()).Log("msg", "piped cache upsert (empty) failed", "query", key, "error", werr)
					}
					return
				}
				_ = level.Warn(ctx.Log()).Log("msg", "piped search failed", "query", key, "error", perr)
				return
			}
			out[i].YoutubeID = id
			if werr := c.Bot.Store.UpsertPipedVideo(budgetCtx, key, id); werr != nil {
				_ = level.Warn(ctx.Log()).Log("msg", "piped cache upsert failed", "query", key, "error", werr)
			}
		}()
	}
	wg.Wait()
	return out
}
