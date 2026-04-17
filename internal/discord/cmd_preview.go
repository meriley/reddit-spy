package discord

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/go-kit/log/level"

	ctxpkg "github.com/meriley/reddit-spy/internal/context"
	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	"github.com/meriley/reddit-spy/internal/evaluator"
	"github.com/meriley/reddit-spy/internal/llm"
	redditJSON "github.com/meriley/reddit-spy/internal/redditJSON"
)

// previewCommandConfig registers /preview_digest — a dry-run that runs the
// shaper + embed builder against a real Reddit post and responds ephemerally
// with the resulting embed. No DB writes, no dedup mutation, nothing sent to
// the channel's regular message history.
func (c *Client) previewCommandConfig() CommandConfig {
	return CommandConfig{
		Command: &discordgo.ApplicationCommand{
			Name:        "preview_digest",
			Description: "Dry-run the LLM digest for a Reddit post. Visible only to you by default.",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "url",
					Description: "Reddit post URL or post ID (e.g. xyz123)",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "public",
					Description: "Post the preview visibly to the channel (default: only you see it).",
					Required:    false,
				},
			},
		},
		Handler: c.previewHandler,
	}
}

func (c *Client) previewHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	var urlArg string
	public := false
	for _, o := range i.ApplicationCommandData().Options {
		switch o.Name {
		case "url":
			urlArg = o.StringValue()
		case "public":
			public = o.BoolValue()
		}
	}

	flags := discordgo.MessageFlagsEphemeral
	if public {
		flags = 0
	}

	// Defer the response — fetching the post + running the LLM easily exceeds
	// Discord's 3-second initial-ack budget. Deferral gives us 15 minutes.
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: flags},
	}); err != nil {
		_ = level.Error(c.Ctx.Log()).Log("msg", "preview_digest: defer failed", "error", err)
		return
	}

	embeds, notice, err := c.buildPreview(c.Ctx, i.ChannelID, urlArg)
	if err != nil {
		_, ferr := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: fmt.Sprintf(":warning: preview failed: %s", err),
			Flags:   flags,
		})
		if ferr != nil {
			_ = level.Error(c.Ctx.Log()).Log("msg", "preview_digest: followup error-reply failed", "error", ferr)
		}
		return
	}

	// Discord enforces a per-MESSAGE 6000-char budget across all embeds in
	// the same message. The music renderer already chunks each section into
	// its own embed to fit the 4096-char description cap, so each embed is
	// safe on its own — we just need to send one followup per embed (not
	// pack them all into one). First followup carries the notice; the rest
	// are embed-only continuations. Hard-cap at 10 followups defensively.
	if len(embeds) == 0 {
		return
	}
	if len(embeds) > 10 {
		embeds = embeds[:10]
	}
	for idx, e := range embeds {
		content := ""
		if idx == 0 {
			content = notice
		}
		if _, ferr := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: content,
			Embeds:  []*discordgo.MessageEmbed{e},
			Flags:   flags,
		}); ferr != nil {
			_ = level.Error(c.Ctx.Log()).Log("msg", "preview_digest: followup failed", "index", idx, "error", ferr)
			return
		}
	}
}

// buildPreview fetches, matches, shapes, and renders — dispatched by the
// rule's mode. Returns the embeds to show, a short notice line, and an error.
func (c *Client) buildPreview(ctx ctxpkg.Ctx, channelExternalID, urlOrID string) ([]*discordgo.MessageEmbed, string, error) {
	postID, err := parseRedditPostID(urlOrID)
	if err != nil {
		return nil, "", err
	}

	post, err := fetchRedditPost(ctx, postID)
	if err != nil {
		return nil, "", fmt.Errorf("fetch reddit post: %w", err)
	}

	ch, err := c.Bot.Store.GetDiscordChannelByExternalID(ctx, channelExternalID)
	if err != nil {
		return nil, "", fmt.Errorf("channel not registered with reddit-spy: %w", err)
	}

	rules, err := c.Bot.Store.GetRulesByChannel(ctx, channelExternalID)
	if err != nil {
		return nil, "", fmt.Errorf("load channel rules: %w", err)
	}
	var rule *dbstore.RuleDetail
	for _, r := range rules {
		if strings.EqualFold(r.Subreddit, post.Subreddit) {
			rule = r
			break
		}
	}
	if rule == nil {
		return nil, "", fmt.Errorf("no rule configured for r/%s in this channel — use /add_subreddit_listener first", post.Subreddit)
	}

	subreddit, err := c.Bot.Store.GetSubredditByExternalID(ctx, post.Subreddit)
	if err != nil {
		return nil, "", fmt.Errorf("subreddit lookup: %w", err)
	}

	phoenix := c.now().In(c.loc)
	dayLocal := time.Date(phoenix.Year(), phoenix.Month(), phoenix.Day(), 0, 0, 0, 0, time.UTC)

	existing, err := c.Bot.Store.GetRollingPost(ctx, ch.ID, subreddit.ID, dayLocal)
	if err != nil {
		return nil, "", fmt.Errorf("rolling-post lookup: %w", err)
	}

	mode := rule.Mode
	if mode == "" {
		mode = dbstore.ModeNarrative
	}

	fakeResult := &evaluator.MatchingEvaluationResult{
		ChannelID: ch.ID,
		RuleID:    rule.ID,
		PostID:    0, // preview: never written to DB
		Post:      post,
		Rule: &dbstore.Rule{
			ID:       rule.ID,
			TargetID: rule.TargetID,
			Exact:    rule.Exact,
			Mode:     mode,
		},
	}

	switch mode {
	case dbstore.ModeMusic:
		return c.previewMusic(ctx, existing, fakeResult, subreddit, dayLocal, rule, post)
	default:
		return c.previewNarrative(ctx, existing, fakeResult, ch, subreddit, dayLocal, rule, post)
	}
}

func (c *Client) previewNarrative(
	ctx ctxpkg.Ctx,
	existing *dbstore.RollingPost,
	fakeResult *evaluator.MatchingEvaluationResult,
	ch *dbstore.DiscordChannel,
	subreddit *dbstore.Subreddit,
	dayLocal time.Time,
	rule *dbstore.RuleDetail,
	post *redditJSONPost,
) ([]*discordgo.MessageEmbed, string, error) {
	pathLabel := "Fresh (first match of the Phoenix day)"
	var title, summary string
	if existing == nil {
		title, summary = c.freshNarrative(ctx, fakeResult)
	} else {
		pathLabel = fmt.Sprintf("Update (today's digest already has %d post(s))", len(existing.IncludedPostIDs))
		title, summary = c.updateNarrative(ctx, existing, fakeResult)
	}
	rp := buildRollingPostRow(existing, fakeResult, ch, subreddit, dayLocal, title, summary)
	embed := buildDigestEmbed(rp, fakeResult, subreddit.ExternalID)
	notice := fmt.Sprintf(
		":microscope: **Preview (narrative)** — nothing was sent to the channel and no DB rows changed.\n"+
			"Path: **%s** · Rule `#%d` matched on `%s` (%s) · r/%s",
		pathLabel, rule.ID, rule.TargetID, ruleMatchLabel(rule.Exact), post.Subreddit,
	)
	return []*discordgo.MessageEmbed{embed}, notice, nil
}

func (c *Client) previewMusic(
	ctx ctxpkg.Ctx,
	existing *dbstore.RollingPost,
	fakeResult *evaluator.MatchingEvaluationResult,
	subreddit *dbstore.Subreddit,
	dayLocal time.Time,
	rule *dbstore.RuleDetail,
	post *redditJSONPost,
) ([]*discordgo.MessageEmbed, string, error) {
	if c.shaper == nil {
		return nil, "", fmt.Errorf("music mode requires an LLM shaper (LLM_BASE_URL unset?)")
	}
	var known []llm.MusicEntry
	if existing != nil {
		decoded, err := decodeMusicEntries(existing.Entries)
		if err != nil {
			return nil, "", err
		}
		known = decoded
	}
	newEntries, err := c.shaper.ShapeMusic(ctx, llm.MusicInput{
		Post:         post,
		KnownEntries: known,
		RuleID:       rule.ID,
		RuleTargetID: rule.TargetID,
		RuleExact:    rule.Exact,
	})
	if err != nil {
		return nil, "", fmt.Errorf("music extraction failed: %w", err)
	}
	rp, merged, err := buildMusicRollingPost(existing, fakeResult, subreddit, dayLocal, newEntries)
	if err != nil {
		return nil, "", err
	}
	merged = mergeListeners(merged, known)
	merged = c.enrichMusicAll(ctx, merged)
	embeds := renderMusicEmbeds(rp, merged, subreddit.ExternalID)
	// Caller sends one followup per embed — same trick the live post uses.
	notice := fmt.Sprintf(
		":microscope: **Preview (music)** — %d new release(s) extracted, %d total in the simulated digest "+
			"across %d section(s). Nothing was sent to the channel and no DB rows changed.\nRule `#%d` on r/%s.",
		len(newEntries), len(merged), len(embeds), rule.ID, post.Subreddit,
	)
	return embeds, notice, nil
}

// redditJSONPost is a local alias for redditJSON.RedditPost so the preview
// helpers' signatures stay short.
type redditJSONPost = redditJSON.RedditPost

func ruleMatchLabel(exact bool) string {
	if exact {
		return "exact"
	}
	return "partial"
}

// parseRedditPostID accepts any of:
//   - full URL:    https://www.reddit.com/r/Metalcore/comments/xyz123/title_slug/
//   - short URL:   https://redd.it/xyz123
//   - bare ID:     xyz123
//
// Reddit post IDs are base36 lowercase, 5–10 characters.
var redditPostIDRe = regexp.MustCompile(`(?:/comments/|redd\.it/|^)([a-z0-9]{5,10})(?:[/?]|$)`)

func parseRedditPostID(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("empty url/id")
	}
	if m := redditPostIDRe.FindStringSubmatch(s); m != nil {
		return m[1], nil
	}
	return "", fmt.Errorf("cannot extract reddit post ID from %q", s)
}

// fetchRedditPost pulls a single post's metadata from Reddit's public JSON
// endpoint. Uses a 15-second hard timeout independent of the caller context
// so the slash-command handler stays responsive.
func fetchRedditPost(parent ctxpkg.Ctx, id string) (*redditJSON.RedditPost, error) {
	url := fmt.Sprintf("https://www.reddit.com/comments/%s.json?limit=1&raw_json=1", id)
	req, err := http.NewRequestWithContext(parent, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "reddit-spy/preview (+gitea.cmtriley.com/mriley/reddit-spy)")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reddit returned HTTP %d", resp.StatusCode)
	}

	// Reddit's comments endpoint returns a 2-element array: [post_listing, comments_listing].
	var listing []struct {
		Data struct {
			Children []struct {
				Data redditJSON.RedditPost `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil {
		return nil, fmt.Errorf("decode reddit json: %w", err)
	}
	if len(listing) == 0 || len(listing[0].Data.Children) == 0 {
		return nil, errors.New("post not found in reddit response")
	}
	p := listing[0].Data.Children[0].Data
	if p.ID == "" {
		return nil, errors.New("reddit returned an empty post id")
	}
	return &p, nil
}
