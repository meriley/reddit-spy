package discord

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/go-kit/log/level"

	ctxpkg "github.com/meriley/reddit-spy/internal/context"
	"github.com/meriley/reddit-spy/internal/llm"
	"github.com/meriley/reddit-spy/internal/qobuz"
)

const (
	qobuzCacheTTL          = 30 * 24 * time.Hour
	qobuzConcurrency       = 2
	qobuzPerRequestTimeout = 9 * time.Second
	qobuzTotalBudget       = 60 * time.Second
)

// enrichMusicQobuzURLs fills MusicEntry.QobuzURL via the qobuz_cache + a
// keyless scrape of qobuz.com/us-en/search. Same failure-is-soft posture as
// Piped and Last.fm: per-entry errors are logged, the line just loses its
// Qobuz link, they don't abort the pass.
func (c *Client) enrichMusicQobuzURLs(ctx ctxpkg.Ctx, entries []llm.MusicEntry) []llm.MusicEntry {
	if c.qobuz == nil || len(entries) == 0 {
		return entries
	}
	out := make([]llm.MusicEntry, len(entries))
	copy(out, entries)

	budgetCtx, cancel := context.WithTimeout(ctx, qobuzTotalBudget)
	defer cancel()

	sem := make(chan struct{}, qobuzConcurrency)
	var wg sync.WaitGroup

	for i := range out {
		if out[i].Artist == "" || out[i].Title == "" {
			continue
		}
		if out[i].QobuzURL != "" {
			continue
		}
		wg.Add(1)
		i := i
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			key := qobuz.QueryKey(out[i].Artist, out[i].Title)

			cachedURL, fetchedAt, ok, err := c.Bot.Store.GetQobuzAlbum(budgetCtx, key)
			if err != nil {
				_ = level.Warn(ctx.Log()).Log("msg", "qobuz cache lookup failed", "query", key, "error", err)
			}
			if ok && time.Since(fetchedAt) < qobuzCacheTTL {
				out[i].QobuzURL = cachedURL
				return
			}

			reqCtx, reqCancel := context.WithTimeout(budgetCtx, qobuzPerRequestTimeout)
			defer reqCancel()
			url, qerr := c.qobuz.SearchFirstAlbum(reqCtx, out[i].Artist, out[i].Title)
			if qerr != nil {
				if errors.Is(qerr, qobuz.ErrNoResult) {
					// Cacheable no-match.
					if werr := c.Bot.Store.UpsertQobuzAlbum(budgetCtx, key, ""); werr != nil {
						_ = level.Warn(ctx.Log()).Log("msg", "qobuz cache upsert (empty) failed", "query", key, "error", werr)
					}
					return
				}
				_ = level.Warn(ctx.Log()).Log("msg", "qobuz search failed", "query", key, "error", qerr)
				return
			}
			out[i].QobuzURL = url
			if werr := c.Bot.Store.UpsertQobuzAlbum(budgetCtx, key, url); werr != nil {
				_ = level.Warn(ctx.Log()).Log("msg", "qobuz cache upsert failed", "query", key, "error", werr)
			}
		}()
	}
	wg.Wait()
	return out
}
