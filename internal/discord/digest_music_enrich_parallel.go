package discord

import (
	"sync"

	ctxpkg "github.com/meriley/reddit-spy/internal/context"
	"github.com/meriley/reddit-spy/internal/llm"
)

// enrichMusicAll runs the three independent enrichment passes (Last.fm
// listeners+tags, Piped YouTube URL, Qobuz album URL) in parallel against
// separate copies of the entry list, then merges the results so per-entry
// fields all land together. Collapses the worst-case wait from the sum of
// their total budgets to the max of them — critical on /preview_digest
// where Discord's "Thinking…" UI gives up around 3 min client-side even
// though the interaction itself allows 15.
func (c *Client) enrichMusicAll(ctx ctxpkg.Ctx, entries []llm.MusicEntry) []llm.MusicEntry {
	if len(entries) == 0 {
		return entries
	}
	var (
		wg        sync.WaitGroup
		lastfmOut []llm.MusicEntry
		pipedOut  []llm.MusicEntry
		qobuzOut  []llm.MusicEntry
	)

	wg.Add(3)
	go func() {
		defer wg.Done()
		lastfmOut = c.enrichMusicListeners(ctx, entries)
	}()
	go func() {
		defer wg.Done()
		pipedOut = c.enrichMusicYouTubeIDs(ctx, entries)
	}()
	go func() {
		defer wg.Done()
		qobuzOut = c.enrichMusicQobuzURLs(ctx, entries)
	}()
	wg.Wait()

	// Merge each pass's outputs into a single slice. Each pass only fills
	// the fields it owns, so take the non-zero value from whichever source
	// has it. Fall back to the input entry (unchanged) when all three
	// passes returned nothing for that slot.
	out := make([]llm.MusicEntry, len(entries))
	copy(out, entries)
	for i := range out {
		if i < len(lastfmOut) {
			if lastfmOut[i].Listeners != 0 {
				out[i].Listeners = lastfmOut[i].Listeners
			}
			if len(lastfmOut[i].Tags) != 0 {
				out[i].Tags = lastfmOut[i].Tags
			}
		}
		if i < len(pipedOut) && pipedOut[i].YoutubeURL != "" {
			out[i].YoutubeURL = pipedOut[i].YoutubeURL
		}
		if i < len(qobuzOut) && qobuzOut[i].QobuzURL != "" {
			out[i].QobuzURL = qobuzOut[i].QobuzURL
		}
	}
	return out
}
