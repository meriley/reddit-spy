package discord

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/go-kit/log/level"

	ctxpkg "github.com/meriley/reddit-spy/internal/context"
	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	"github.com/meriley/reddit-spy/internal/evaluator"
	"github.com/meriley/reddit-spy/internal/llm"
)

// maxDescRunes is the practical per-embed description budget. Discord's hard
// limit is 4096 runes; we leave slack so a single oversize line can't push
// us over.
const maxDescRunes = 3900

func decodeMusicEntries(raw []byte) ([]llm.MusicEntry, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" || s == "[]" {
		return nil, nil
	}
	var out []llm.MusicEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode music entries: %w", err)
	}
	return out, nil
}

func encodeMusicEntries(entries []llm.MusicEntry) ([]byte, error) {
	if len(entries) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(entries)
}

// buildMusicRollingPost produces the next rolling_posts row for a music-mode
// match, merging newly-extracted entries into any prior ones (prior first,
// new appended, dedup on normalized key).
func buildMusicRollingPost(
	existing *dbstore.RollingPost,
	result *evaluator.MatchingEvaluationResult,
	subreddit *dbstore.Subreddit,
	dayLocal time.Time,
	newEntries []llm.MusicEntry,
) (dbstore.RollingPost, []llm.MusicEntry, error) {
	rp := dbstore.RollingPost{
		ChannelID:       result.ChannelID,
		SubredditID:     subreddit.ID,
		DayLocal:        dayLocal,
		Mode:            dbstore.ModeMusic,
		LatestScore:     result.Post.Score,
		LatestComments:  result.Post.NumComments,
		LatestURL:       result.Post.URL,
		LatestThumbnail: result.Post.Thumbnail,
	}

	var prior []llm.MusicEntry
	if existing != nil {
		decoded, err := decodeMusicEntries(existing.Entries)
		if err != nil {
			return rp, nil, err
		}
		prior = decoded
		rp.DiscordMessageIDs = append([]string(nil), existing.DiscordMessageIDs...)
		rp.IncludedPostIDs = appendUnique(existing.IncludedPostIDs, result.Post.ID)
		rp.IncludedRuleIDs = appendUniqueInt(existing.IncludedRuleIDs, result.RuleID)
	} else {
		rp.IncludedPostIDs = []string{result.Post.ID}
		rp.IncludedRuleIDs = []int{result.RuleID}
	}

	merged := append([]llm.MusicEntry(nil), prior...)
	seen := make(map[string]struct{}, len(merged))
	for _, e := range merged {
		seen[llm.MusicDedupeKey(e)] = struct{}{}
	}
	for _, e := range newEntries {
		k := llm.MusicDedupeKey(e)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		merged = append(merged, e)
	}

	encoded, err := encodeMusicEntries(merged)
	if err != nil {
		return rp, nil, err
	}
	rp.Entries = encoded
	return rp, merged, nil
}

// renderMusicEmbeds groups entries by kind (albums → EPs → singles) and lays
// them out as compact lines under section headers. One embed per Discord
// message; the renderer spills into a second/third embed when a single
// description would exceed the 4000-rune budget.
func renderMusicEmbeds(rp dbstore.RollingPost, entries []llm.MusicEntry, subredditExternalID string) []*discordgo.MessageEmbed {
	lines := groupAndFormatMusic(entries)

	totalStr := fmt.Sprintf("%d release", len(entries))
	if len(entries) != 1 {
		totalStr = fmt.Sprintf("%d releases", len(entries))
	}
	dayStr := rp.DayLocal.Format("2006-01-02")
	headerTitle := fmt.Sprintf("r/%s — music digest", subredditExternalID)

	chunks := chunkLines(lines, maxDescRunes)
	if len(chunks) == 0 {
		chunks = []string{"(no releases extracted yet)"}
	}

	embeds := make([]*discordgo.MessageEmbed, 0, len(chunks))
	for i, body := range chunks {
		title := headerTitle
		if len(chunks) > 1 {
			title = fmt.Sprintf("%s (%d/%d)", headerTitle, i+1, len(chunks))
		}
		e := &discordgo.MessageEmbed{
			Type:        discordgo.EmbedTypeRich,
			Color:       embedColorReddit,
			Title:       truncateUTF8(title, 256),
			Description: body,
			Footer: &discordgo.MessageEmbedFooter{
				Text: fmt.Sprintf("%s • %s", totalStr, dayStr),
			},
		}
		if i == 0 {
			e.URL = rp.LatestURL
			e.Author = &discordgo.MessageEmbedAuthor{
				Name: fmt.Sprintf("r/%s", subredditExternalID),
			}
		}
		embeds = append(embeds, e)
	}
	return embeds
}

// groupAndFormatMusic sorts entries into album / ep / single buckets (in
// that order), popularity-sorts within each bucket (Last.fm listener count
// desc; unknowns fall to the bottom in source order), and renders one tight
// line per entry.
func groupAndFormatMusic(entries []llm.MusicEntry) []string {
	type bucket struct {
		header  string
		members []llm.MusicEntry
	}
	buckets := []*bucket{
		{header: "**Albums**"},
		{header: "**EPs**"},
		{header: "**Singles**"},
	}
	for _, e := range entries {
		switch strings.ToLower(e.Kind) {
		case "album":
			buckets[0].members = append(buckets[0].members, e)
		case "ep":
			buckets[1].members = append(buckets[1].members, e)
		default:
			buckets[2].members = append(buckets[2].members, e)
		}
	}
	for _, b := range buckets {
		sortByPopularity(b.members)
	}

	var lines []string
	for _, b := range buckets {
		if len(b.members) == 0 {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "") // blank line between sections
		}
		lines = append(lines, b.header)
		for _, e := range b.members {
			lines = append(lines, formatMusicLineCompact(e))
		}
	}
	return lines
}

// sortByPopularity orders entries by Last.fm listener count desc. Entries
// with zero (unknown) sink to the bottom; within each partition we preserve
// source order (stable sort) so the curator's ordering survives for ties.
func sortByPopularity(entries []llm.MusicEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Listeners == 0 && b.Listeners != 0 {
			return false
		}
		if a.Listeners != 0 && b.Listeners == 0 {
			return true
		}
		return a.Listeners > b.Listeners
	})
}

// formatMusicLineCompact renders one entry as:
//
//	**Artist** – [Title](https://music.youtube.com/…) `tag1, tag2`
//
// The URL may be a watch?v=… (song) or a playlist?list=… (full album playlist).
// When there's no Piped-sourced URL the title stays unlinked. Tags come from
// Last.fm and are omitted when absent.
func formatMusicLineCompact(e llm.MusicEntry) string {
	title := e.Title
	if e.YoutubeURL != "" {
		// Escape any literal ']' in the title so Discord's markdown parser
		// doesn't truncate the link text at it.
		safe := strings.ReplaceAll(e.Title, "]", " ")
		title = fmt.Sprintf("[%s](%s)", safe, e.YoutubeURL)
	}
	base := fmt.Sprintf("**%s** – %s", e.Artist, title)
	if len(e.Tags) == 0 {
		return base
	}
	top := e.Tags
	if len(top) > 2 {
		top = top[:2]
	}
	return fmt.Sprintf("%s `%s`", base, strings.Join(top, ", "))
}

// chunkLines groups lines into strings each ≤ maxRunes, newline-separated.
// An oversize single line passes through untrimmed — the shaper is expected
// to keep individual entries short.
func chunkLines(lines []string, maxRunes int) []string {
	if len(lines) == 0 {
		return nil
	}
	var out []string
	var cur strings.Builder
	curRunes := 0
	for _, line := range lines {
		addLen := runeCount(line)
		sep := 0
		if cur.Len() > 0 {
			sep = 1
		}
		if curRunes+sep+addLen > maxRunes && cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
			curRunes = 0
			sep = 0
		}
		if sep == 1 {
			cur.WriteByte('\n')
			curRunes++
		}
		cur.WriteString(line)
		curRunes += addLen
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// handleMusicMatch is the SendMessage branch for music-mode rules.
func (c *Client) handleMusicMatch(
	ctx ctxpkg.Ctx,
	existing *dbstore.RollingPost,
	result *evaluator.MatchingEvaluationResult,
	ch *dbstore.DiscordChannel,
	subreddit *dbstore.Subreddit,
	dayLocal time.Time,
) error {
	if c.shaper == nil {
		_ = level.Warn(ctx.Log()).Log("msg", "music mode requires an LLM shaper; skipping match")
		return nil
	}

	var known []llm.MusicEntry
	if existing != nil {
		decoded, err := decodeMusicEntries(existing.Entries)
		if err != nil {
			return err
		}
		known = decoded
	}

	newEntries, err := c.shaper.ShapeMusic(ctx, llm.MusicInput{
		Post:         result.Post,
		KnownEntries: known,
		RuleID:       result.RuleID,
		RuleTargetID: result.Rule.TargetID,
		RuleExact:    result.Rule.Exact,
	})
	if err != nil {
		_ = level.Warn(ctx.Log()).Log("msg", "music shape failed; leaving digest unchanged", "error", err)
		// Record the notification so we don't retry the same post every poll tick.
		if _, nerr := c.Bot.Store.InsertNotification(ctx, result.PostID, result.ChannelID, result.RuleID); nerr != nil {
			return fmt.Errorf("insert notification after music shape failure: %w", nerr)
		}
		return nil
	}

	rp, merged, err := buildMusicRollingPost(existing, result, subreddit, dayLocal, newEntries)
	if err != nil {
		return fmt.Errorf("build music rolling post: %w", err)
	}

	if len(merged) == 0 && existing == nil {
		// Nothing to show, and no prior message — just record the notification
		// so we don't reprocess this post on the next poll.
		if _, nerr := c.Bot.Store.InsertNotification(ctx, result.PostID, result.ChannelID, result.RuleID); nerr != nil {
			return fmt.Errorf("insert notification for empty music match: %w", nerr)
		}
		return nil
	}

	// Enrichment passes — each is best-effort; failures leave the entry
	// un-annotated and fall back to source order / plain text.
	merged = mergeListeners(merged, known)
	merged = c.enrichMusicListeners(ctx, merged)
	merged = c.enrichMusicYouTubeIDs(ctx, merged)
	// Persist enriched signals so we don't re-lookup on the next same-day match.
	if enriched, eerr := encodeMusicEntries(merged); eerr == nil {
		rp.Entries = enriched
	}

	embeds := renderMusicEmbeds(rp, merged, subreddit.ExternalID)

	var priorIDs []string
	if existing != nil {
		priorIDs = existing.DiscordMessageIDs
	}
	newIDs, err := c.syncMessages(ctx, ch, priorIDs, embeds)
	if err != nil {
		return err
	}
	rp.DiscordMessageIDs = newIDs

	if _, err := c.Bot.Store.UpsertRollingPost(ctx, rp); err != nil {
		return fmt.Errorf("upsert music rolling post: %w", err)
	}
	if _, err := c.Bot.Store.InsertNotification(ctx, result.PostID, result.ChannelID, result.RuleID); err != nil {
		return fmt.Errorf("insert notification for music match: %w", err)
	}
	return nil
}

// syncMessages reconciles the list of Discord messages that back a rolling
// digest with the set of embeds we want displayed. One embed per message,
// indexed by position: overlap → edit, extras → send, missing → delete.
func (c *Client) syncMessages(
	ctx ctxpkg.Ctx,
	ch *dbstore.DiscordChannel,
	priorIDs []string,
	embeds []*discordgo.MessageEmbed,
) ([]string, error) {
	newIDs := make([]string, 0, len(embeds))
	for i, embed := range embeds {
		if i < len(priorIDs) && priorIDs[i] != "" {
			edited, err := c.sender.ChannelMessageEditComplex(&discordgo.MessageEdit{
				Channel: ch.ExternalID,
				ID:      priorIDs[i],
				Embeds:  &[]*discordgo.MessageEmbed{embed},
			})
			if isMessageGone(err) {
				msg, sendErr := c.sender.ChannelMessageSendComplex(ch.ExternalID, &discordgo.MessageSend{
					Embeds: []*discordgo.MessageEmbed{embed},
				})
				if sendErr != nil {
					return nil, fmt.Errorf("fallback send after edit-404: %w", sendErr)
				}
				newIDs = append(newIDs, msg.ID)
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("edit message %d: %w", i, err)
			}
			newIDs = append(newIDs, edited.ID)
			continue
		}
		msg, err := c.sender.ChannelMessageSendComplex(ch.ExternalID, &discordgo.MessageSend{
			Embeds: []*discordgo.MessageEmbed{embed},
		})
		if err != nil {
			return nil, fmt.Errorf("send message %d: %w", i, err)
		}
		newIDs = append(newIDs, msg.ID)
	}

	for i := len(embeds); i < len(priorIDs); i++ {
		if priorIDs[i] == "" {
			continue
		}
		if err := c.messageDelete(ch.ExternalID, priorIDs[i]); err != nil {
			_ = level.Warn(ctx.Log()).Log(
				"msg", "failed to delete stale spill message (non-fatal)",
				"message_id", priorIDs[i], "error", err,
			)
		}
	}
	return newIDs, nil
}

// messageDelete delegates to ChannelMessageDelete when the sender supports it.
// Tests usually don't; the shrink path then silently no-ops (which is fine —
// the stale embed just sits there until the next day rolls over).
func (c *Client) messageDelete(channelID, messageID string) error {
	type deleter interface {
		ChannelMessageDelete(channelID, messageID string, options ...discordgo.RequestOption) error
	}
	if d, ok := c.sender.(deleter); ok {
		return d.ChannelMessageDelete(channelID, messageID)
	}
	return nil
}
