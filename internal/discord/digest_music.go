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
		// Carry the existing row's identity and opening-time fields so
		// UpsertRollingPost takes the UPDATE-by-id path.
		rp.ID = existing.ID
		rp.WindowStart = existing.WindowStart
		rp.DayLocal = existing.DayLocal
		rp.SubredditID = existing.SubredditID // opening sub stays for display
		rp.SubredditIDs = appendUniqueInt(existing.SubredditIDs, subreddit.ID)
		rp.DiscordMessageIDs = append([]string(nil), existing.DiscordMessageIDs...)
		rp.IncludedPostIDs = appendUnique(existing.IncludedPostIDs, result.Post.ID)
		rp.IncludedRuleIDs = appendUniqueInt(existing.IncludedRuleIDs, result.RuleID)
	} else {
		rp.SubredditID = subreddit.ID
		rp.SubredditIDs = []int{subreddit.ID}
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

// cardTopN caps each inline bucket on the parent card. Discord's per-field
// value limit is 1024 runes — 5 compact lines stays well under that budget
// even with long artist/title combos.
const cardTopN = 5

// fieldValueBudget — Discord hard limit is 1024; leave slack for the ellipsis
// tail ("… +N more"). The parent card trims to this.
const fieldValueBudget = 980

// renderMusicCard builds the parent-message embed — a compact "at-a-glance"
// card with three inline columns (Albums / EPs / Singles) showing the top
// cardTopN entries per bucket. The full per-release spill lives in the thread
// attached to this message (see renderMusicThreadEmbeds).
func renderMusicCard(rp dbstore.RollingPost, entries []llm.MusicEntry, subredditNames []string) *discordgo.MessageEmbed {
	totalStr := fmt.Sprintf("%d release", len(entries))
	if len(entries) != 1 {
		totalStr = fmt.Sprintf("%d releases", len(entries))
	}
	dayStr := rp.DayLocal.Format("2006-01-02")

	subsHeader := "music digest"
	if joined := joinSubNames(subredditNames); joined != "" {
		subsHeader = joined + " — music digest"
	}

	albums, eps, singles := splitByKind(entries)
	sortByPopularity(albums)
	sortByPopularity(eps)
	sortByPopularity(singles)

	embed := &discordgo.MessageEmbed{
		Type:  discordgo.EmbedTypeRich,
		Color: embedColorReddit,
		Title: truncateUTF8(subsHeader, 256),
		URL:   rp.LatestURL,
		Author: &discordgo.MessageEmbedAuthor{
			Name: subsHeader,
		},
		Description: fmt.Sprintf("%s • full list in the thread below", totalStr),
		Fields: []*discordgo.MessageEmbedField{
			buildCardField("Albums", albums),
			buildCardField("EPs", eps),
			buildCardField("Singles", singles),
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("opened %s", dayStr),
		},
	}
	return embed
}

// buildCardField renders one of the three inline columns. Displays up to
// cardTopN entries, each as "**Artist** — Title" (no links in the card to
// keep the column narrow and scannable). Overflow gets a trailing
// "… +N more" line so readers know to open the thread.
func buildCardField(name string, entries []llm.MusicEntry) *discordgo.MessageEmbedField {
	if len(entries) == 0 {
		return &discordgo.MessageEmbedField{
			Name:   name,
			Value:  "_—_",
			Inline: true,
		}
	}
	shown := entries
	if len(shown) > cardTopN {
		shown = shown[:cardTopN]
	}
	var b strings.Builder
	for i, e := range shown {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "**%s** — %s", truncateUTF8(e.Artist, 48), truncateUTF8(e.Title, 48))
	}
	if extra := len(entries) - len(shown); extra > 0 {
		fmt.Fprintf(&b, "\n_+%d more_", extra)
	}
	val := b.String()
	if runeCount(val) > fieldValueBudget {
		val = truncateUTF8(val, fieldValueBudget-1) + "…"
	}
	return &discordgo.MessageEmbedField{
		Name:   fmt.Sprintf("%s (%d)", name, len(entries)),
		Value:  val,
		Inline: true,
	}
}

// splitByKind returns (albums, eps, singles) in input order. "single" is the
// default bucket for anything not tagged album or ep.
func splitByKind(entries []llm.MusicEntry) ([]llm.MusicEntry, []llm.MusicEntry, []llm.MusicEntry) {
	var albums, eps, singles []llm.MusicEntry
	for _, e := range entries {
		switch strings.ToLower(e.Kind) {
		case "album":
			albums = append(albums, e)
		case "ep":
			eps = append(eps, e)
		default:
			singles = append(singles, e)
		}
	}
	return albums, eps, singles
}

// renderMusicThreadEmbeds builds the set of embeds that live as replies inside
// the digest's thread. Each embed is a section (Albums, EPs, Singles) with
// per-release links/tags; sections that overflow the 4000-rune description
// budget spill into additional embeds (i/N suffix on the title).
func renderMusicThreadEmbeds(rp dbstore.RollingPost, entries []llm.MusicEntry) []*discordgo.MessageEmbed {
	lines := groupAndFormatMusic(entries)
	chunks := chunkLines(lines, maxDescRunes)
	if len(chunks) == 0 {
		return nil
	}

	totalStr := fmt.Sprintf("%d release", len(entries))
	if len(entries) != 1 {
		totalStr = fmt.Sprintf("%d releases", len(entries))
	}
	dayStr := rp.DayLocal.Format("2006-01-02")

	embeds := make([]*discordgo.MessageEmbed, 0, len(chunks))
	for i, body := range chunks {
		title := "Full list"
		if len(chunks) > 1 {
			title = fmt.Sprintf("Full list (%d/%d)", i+1, len(chunks))
		}
		embeds = append(embeds, &discordgo.MessageEmbed{
			Type:        discordgo.EmbedTypeRich,
			Color:       embedColorReddit,
			Title:       title,
			Description: body,
			Footer: &discordgo.MessageEmbedFooter{
				Text: fmt.Sprintf("%s • %s", totalStr, dayStr),
			},
		})
	}
	return embeds
}

// resolveSubredditNames maps a slice of internal subreddit IDs to their
// external names, preserving input order. Unknown IDs are silently dropped
// (renderer degrades to "music digest" rather than blowing up). One DB
// round-trip per call via GetSubreddits; subreddit cardinality is tiny
// (a handful of subs per channel) so the full-list scan is fine.
func (c *Client) resolveSubredditNames(ctx ctxpkg.Ctx, ids []int) []string {
	if len(ids) == 0 {
		return nil
	}
	subs, err := c.Bot.Store.GetSubreddits(ctx)
	if err != nil {
		_ = level.Warn(ctx.Log()).Log("msg", "resolve subreddit names failed; falling back to generic header", "error", err)
		return nil
	}
	byID := make(map[int]string, len(subs))
	for _, s := range subs {
		byID[s.ID] = s.ExternalID
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if name, ok := byID[id]; ok && name != "" {
			out = append(out, name)
		}
	}
	return out
}

// joinSubNames turns a slice of subreddit external IDs into "r/a, r/b, r/c".
// Empty input returns empty string. Blank entries are skipped.
func joinSubNames(names []string) string {
	parts := make([]string, 0, len(names))
	for _, n := range names {
		if n == "" {
			continue
		}
		parts = append(parts, "r/"+n)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
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
//	**Artist** – [Title](music.youtube.com/…) · [Q](qobuz.com/…) `tag1, tag2`
//
// The YouTube URL may be watch?v=… (song) or playlist?list=… (album). The
// Qobuz link is appended only for subscribers — it's elided when absent.
// Title stays unlinked if no YouTube URL was resolved. Tags are omitted when
// absent.
func formatMusicLineCompact(e llm.MusicEntry) string {
	title := e.Title
	if e.YoutubeURL != "" {
		// Escape any literal ']' in the title so Discord's markdown parser
		// doesn't truncate the link text at it.
		safe := strings.ReplaceAll(e.Title, "]", " ")
		title = fmt.Sprintf("[%s](%s)", safe, e.YoutubeURL)
	}
	base := fmt.Sprintf("**%s** – %s", e.Artist, title)
	if e.QobuzURL != "" {
		base += fmt.Sprintf(" · [Q](%s)", e.QobuzURL)
	}
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

// handleMusicMatch is the SendMessage branch for music-mode rules. Output
// shape: parent card in the channel + a thread attached to that card that
// holds the full per-release spill. Same-window matches edit the card in
// place and sync thread-reply messages by index.
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
	// un-annotated and fall back to source order / plain text. The three
	// passes run in parallel (enrichMusicAll) so the user-facing latency
	// is max(lastfm, piped, qobuz), not their sum.
	merged = mergeListeners(merged, known)
	merged = c.enrichMusicAll(ctx, merged)
	// Persist enriched signals so we don't re-lookup on the next same-day match.
	if enriched, eerr := encodeMusicEntries(merged); eerr == nil {
		rp.Entries = enriched
	}

	subNames := c.resolveSubredditNames(ctx, rp.SubredditIDs)
	card := renderMusicCard(rp, merged, subNames)
	threadEmbeds := renderMusicThreadEmbeds(rp, merged)

	// 1. Parent card → channel. Edit prior id if we have one, else send fresh.
	var priorParentIDs []string
	if existing != nil {
		priorParentIDs = existing.DiscordMessageIDs
	}
	parentIDs, err := c.syncParentCard(ctx, ch.ExternalID, priorParentIDs, card)
	if err != nil {
		return fmt.Errorf("sync parent card: %w", err)
	}
	rp.DiscordMessageIDs = parentIDs
	parentMsgID := ""
	if len(parentIDs) > 0 {
		parentMsgID = parentIDs[0]
	}

	// 2. Thread → attached to the parent message. Create on first open;
	//    reuse (un-archive if needed) on subsequent window matches.
	threadID := ""
	if existing != nil {
		threadID = existing.ThreadID
	}
	threadID, err = c.ensureThread(ctx, ch.ExternalID, parentMsgID, threadID, subNames)
	if err != nil {
		return fmt.Errorf("ensure digest thread: %w", err)
	}
	rp.ThreadID = threadID

	var priorThreadReplies []string
	if existing != nil {
		priorThreadReplies = existing.ThreadMessageIDs
	}
	threadReplyIDs, err := c.syncThreadReplies(ctx, threadID, priorThreadReplies, threadEmbeds)
	if err != nil {
		return fmt.Errorf("sync thread replies: %w", err)
	}
	rp.ThreadMessageIDs = threadReplyIDs

	if _, err := c.Bot.Store.UpsertRollingPost(ctx, rp); err != nil {
		return fmt.Errorf("upsert music rolling post: %w", err)
	}
	if _, err := c.Bot.Store.InsertNotification(ctx, result.PostID, result.ChannelID, result.RuleID); err != nil {
		return fmt.Errorf("insert notification for music match: %w", err)
	}
	return nil
}

// syncParentCard sends (or edits) exactly one message in the channel to carry
// the digest's at-a-glance card. Returns a single-element ID slice.
func (c *Client) syncParentCard(
	ctx ctxpkg.Ctx,
	channelID string,
	priorIDs []string,
	card *discordgo.MessageEmbed,
) ([]string, error) {
	prior := ""
	if len(priorIDs) > 0 {
		prior = priorIDs[0]
	}

	if prior == "" {
		msg, err := c.sender.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
			Embeds: []*discordgo.MessageEmbed{card},
		})
		if err != nil {
			return nil, fmt.Errorf("send parent card: %w", err)
		}
		return []string{msg.ID}, nil
	}

	edited, err := c.sender.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel: channelID,
		ID:      prior,
		Embeds:  &[]*discordgo.MessageEmbed{card},
	})
	if isMessageGone(err) {
		_ = level.Warn(ctx.Log()).Log(
			"msg", "parent card missing; sending fresh",
			"channel", channelID, "message_id", prior,
		)
		msg, sendErr := c.sender.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
			Embeds: []*discordgo.MessageEmbed{card},
		})
		if sendErr != nil {
			return nil, fmt.Errorf("fallback send after parent-card 404: %w", sendErr)
		}
		return []string{msg.ID}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("edit parent card: %w", err)
	}
	// Drop any stale extras (previous release wrote multiple channel
	// messages back when each section got its own message).
	for i := 1; i < len(priorIDs); i++ {
		if priorIDs[i] == "" {
			continue
		}
		if derr := c.sender.ChannelMessageDelete(channelID, priorIDs[i]); derr != nil {
			_ = level.Warn(ctx.Log()).Log(
				"msg", "failed to delete stale channel message from pre-thread layout (non-fatal)",
				"message_id", priorIDs[i], "error", derr,
			)
		}
	}
	return []string{edited.ID}, nil
}

// ensureThread returns a usable thread_id for the digest. If priorThreadID is
// empty, creates a thread attached to parentMsgID (which lets "Open in thread"
// work from the card). If priorThreadID is present, un-archives it when
// Discord has auto-archived it after the 7-day idle window, so the next send
// lands in the same thread instead of a new one.
func (c *Client) ensureThread(
	ctx ctxpkg.Ctx,
	channelID, parentMsgID, priorThreadID string,
	subredditNames []string,
) (string, error) {
	if priorThreadID != "" {
		// Best-effort un-archive. Discord silently no-ops when the thread is
		// already active; a permission/404 error here is non-fatal because
		// send into the thread will surface the real failure.
		if _, err := c.sender.ChannelEdit(priorThreadID, &discordgo.ChannelEdit{
			Archived:            ptrBool(false),
			AutoArchiveDuration: threadAutoArchiveMinutes,
		}); err != nil && !isMessageGone(err) {
			_ = level.Warn(ctx.Log()).Log(
				"msg", "un-archive thread failed (non-fatal, will still try to send)",
				"thread_id", priorThreadID, "error", err,
			)
		}
		return priorThreadID, nil
	}
	if parentMsgID == "" {
		return "", fmt.Errorf("cannot start thread: no parent message id")
	}
	name := threadName(subredditNames)
	th, err := c.sender.MessageThreadStart(channelID, parentMsgID, name, threadAutoArchiveMinutes)
	if err != nil {
		return "", fmt.Errorf("start thread: %w", err)
	}
	return th.ID, nil
}

// threadName builds the sidebar label for the digest thread. Discord caps
// thread names at 100 runes; our fallback-safe default stays well under it.
func threadName(subredditNames []string) string {
	joined := joinSubNames(subredditNames)
	if joined == "" {
		return "music digest"
	}
	name := joined + " — releases"
	return truncateUTF8(name, 100)
}

func ptrBool(b bool) *bool { return &b }

// syncThreadReplies reconciles the thread's reply messages with the set of
// embeds we want displayed. Same index-aligned logic as the old
// syncMessages, but scoped to a thread (threads ARE channels on the Discord
// API, so the channel-message endpoints all accept a thread id).
func (c *Client) syncThreadReplies(
	ctx ctxpkg.Ctx,
	threadID string,
	priorIDs []string,
	embeds []*discordgo.MessageEmbed,
) ([]string, error) {
	if threadID == "" {
		// No thread yet (e.g. parent send succeeded but thread creation is
		// happening on a later tick). Caller has already logged; nothing to do.
		return nil, nil
	}
	newIDs := make([]string, 0, len(embeds))
	for i, embed := range embeds {
		if i < len(priorIDs) && priorIDs[i] != "" {
			edited, err := c.sender.ChannelMessageEditComplex(&discordgo.MessageEdit{
				Channel: threadID,
				ID:      priorIDs[i],
				Embeds:  &[]*discordgo.MessageEmbed{embed},
			})
			if isMessageGone(err) {
				msg, sendErr := c.sender.ChannelMessageSendComplex(threadID, &discordgo.MessageSend{
					Embeds: []*discordgo.MessageEmbed{embed},
				})
				if sendErr != nil {
					return nil, fmt.Errorf("fallback send after thread edit-404: %w", sendErr)
				}
				newIDs = append(newIDs, msg.ID)
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("edit thread message %d: %w", i, err)
			}
			newIDs = append(newIDs, edited.ID)
			continue
		}
		msg, err := c.sender.ChannelMessageSendComplex(threadID, &discordgo.MessageSend{
			Embeds: []*discordgo.MessageEmbed{embed},
		})
		if err != nil {
			return nil, fmt.Errorf("send thread message %d: %w", i, err)
		}
		newIDs = append(newIDs, msg.ID)
	}

	for i := len(embeds); i < len(priorIDs); i++ {
		if priorIDs[i] == "" {
			continue
		}
		if err := c.sender.ChannelMessageDelete(threadID, priorIDs[i]); err != nil {
			_ = level.Warn(ctx.Log()).Log(
				"msg", "failed to delete stale thread-reply message (non-fatal)",
				"message_id", priorIDs[i], "error", err,
			)
		}
	}
	return newIDs, nil
}
