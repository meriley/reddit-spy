package discord

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"github.com/go-kit/log/level"

	ctxpkg "github.com/meriley/reddit-spy/internal/context"
	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	"github.com/meriley/reddit-spy/internal/evaluator"
	"github.com/meriley/reddit-spy/internal/llm"
	"github.com/meriley/reddit-spy/redditDiscordBot"
)

const embedColorReddit = 0xFF4500

// PhoenixTZ is the timezone in which "today" is computed for rolling digests.
// Arizona does not observe daylight saving time, so the offset is a stable UTC-7.
const PhoenixTZ = "America/Phoenix"

// Shaper is the slice of llm.Shaper the Discord client depends on. Keeping it
// as an interface lets tests inject deterministic outputs without spinning up
// a real LLM client.
type Shaper interface {
	ShapeFresh(ctx ctxpkg.Ctx, in llm.FreshInput) (llm.Output, error)
	ShapeUpdate(ctx ctxpkg.Ctx, in llm.UpdateInput) (llm.Output, error)
}

// MessageSender isolates the two discordgo.Session methods SendMessage needs,
// so tests can stub them without a live Discord connection.
type MessageSender interface {
	ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend, options ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelMessageEditComplex(m *discordgo.MessageEdit, options ...discordgo.RequestOption) (*discordgo.Message, error)
}

// shaperAdapter bridges ctxpkg.Ctx (the app's logger-carrying context) to the
// plain context.Context the llm.Shaper expects.
type shaperAdapter struct {
	inner *llm.Shaper
}

func (a shaperAdapter) ShapeFresh(ctx ctxpkg.Ctx, in llm.FreshInput) (llm.Output, error) {
	return a.inner.ShapeFresh(ctx, in)
}

func (a shaperAdapter) ShapeUpdate(ctx ctxpkg.Ctx, in llm.UpdateInput) (llm.Output, error) {
	return a.inner.ShapeUpdate(ctx, in)
}

type Client struct {
	Ctx    ctxpkg.Ctx
	Client *discordgo.Session
	Bot    *redditDiscordBot.RedditDiscordBot

	// shaper is the optional LLM wrapper. When nil (e.g. LLM_BASE_URL unset),
	// SendMessage falls back to raw-selftext behaviour so reddit-spy still
	// functions as a pass-through notifier without an LLM backend.
	shaper Shaper

	// sender captures the discordgo methods SendMessage uses. Defaults to
	// Client but is overridable by tests.
	sender MessageSender

	// loc is the tz used to compute dayLocal. Loaded once at New() time.
	loc *time.Location

	// now is injectable for deterministic tests. Defaults to time.Now.
	now func() time.Time
}

// Option configures optional Client fields post-construction.
type Option func(*Client)

// WithShaper attaches an LLM shaper to the Client.
func WithShaper(s *llm.Shaper) Option {
	return func(c *Client) {
		if s != nil {
			c.shaper = shaperAdapter{inner: s}
		}
	}
}

// WithShaperInterface attaches any Shaper-compatible value. Tests use this.
func WithShaperInterface(s Shaper) Option {
	return func(c *Client) { c.shaper = s }
}

// WithSender overrides the default MessageSender (used by tests).
func WithSender(m MessageSender) Option {
	return func(c *Client) { c.sender = m }
}

// WithNow overrides the clock used to compute dayLocal (used by tests).
func WithNow(now func() time.Time) Option {
	return func(c *Client) { c.now = now }
}

func New(ctx ctxpkg.Ctx, bot *redditDiscordBot.RedditDiscordBot, opts ...Option) (*Client, error) {
	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("DISCORD_TOKEN environment variable is required")
	}

	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("failed to create discord session: %w", err)
	}
	dg.Identify.Intents = discordgo.IntentsGuilds
	dg.ShouldReconnectOnError = true
	dg.ShouldRetryOnRateLimit = true
	dg.StateEnabled = true
	dg.MaxRestRetries = 3
	err = dg.Open()
	if err != nil {
		return nil, fmt.Errorf("error opening discord session: %w", err)
	}

	loc, err := time.LoadLocation(PhoenixTZ)
	if err != nil {
		return nil, fmt.Errorf("failed to load %s timezone: %w", PhoenixTZ, err)
	}

	client := &Client{
		Ctx:    ctx,
		Client: dg,
		Bot:    bot,
		sender: dg,
		loc:    loc,
		now:    time.Now,
	}
	for _, opt := range opts {
		opt(client)
	}

	if err := client.RegisterCommands(); err != nil {
		return nil, err
	}

	return client, nil
}

// NewForTest builds a Client with no Discord session for unit tests. It skips
// RegisterCommands and takes the sender/shaper/clock directly as options.
func NewForTest(ctx ctxpkg.Ctx, bot *redditDiscordBot.RedditDiscordBot, opts ...Option) *Client {
	loc, _ := time.LoadLocation(PhoenixTZ)
	c := &Client{
		Ctx: ctx,
		Bot: bot,
		loc: loc,
		now: time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) Close() error {
	if c.Client == nil {
		return nil
	}
	return c.Client.Close()
}

type CommandConfig struct {
	Command *discordgo.ApplicationCommand
	Handler func(s *discordgo.Session, i *discordgo.InteractionCreate)
}

func (c *Client) RegisterCommands() error {
	commands := []CommandConfig{
		c.addSubredditListenerCommandConfig(),
		c.listRulesCommandConfig(),
		c.deleteRuleCommandConfig(),
		c.editRuleCommandConfig(),
		c.pingCommandConfig(),
		c.statusCommandConfig(),
		c.helpCommandConfig(),
		c.previewCommandConfig(),
	}

	commandHandlers := make(map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate))
	for _, cmdConfig := range commands {
		_, err := c.Client.ApplicationCommandCreate(c.Client.State.Application.ID, "", cmdConfig.Command)
		if err != nil {
			return fmt.Errorf("failed to create application command %q: %w", cmdConfig.Command.Name, err)
		}
		commandHandlers[cmdConfig.Command.Name] = cmdConfig.Handler
	}

	c.Client.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			if handler, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
				handler(s, i)
			}
		case discordgo.InteractionMessageComponent:
			c.componentHandler(s, i)
		}
	})

	return nil
}

// SendMessage handles a rule-match event by either sending a fresh rolling
// digest for the (channel, subreddit, day) triple or editing the existing one
// in place. Dedupe on (post, channel, rule) still applies — a given Reddit
// post can only contribute to the digest once.
func (c *Client) SendMessage(ctx ctxpkg.Ctx, result *evaluator.MatchingEvaluationResult) error {
	count, err := c.Bot.Store.GetNotificationCount(ctx, result.PostID, result.ChannelID, result.RuleID)
	if err != nil {
		return fmt.Errorf("unable to get notification count: %w", err)
	}
	if count > 0 {
		return nil
	}

	ch, err := c.Bot.Store.GetDiscordChannel(ctx, result.ChannelID)
	if err != nil {
		return fmt.Errorf("failed to get discord channel for id %d: %w", result.ChannelID, err)
	}

	// Derive the Phoenix calendar day directly from Y/M/D — Truncate(24h) would
	// snap to UTC midnight, not Phoenix midnight, shifting the day backwards
	// for wall-clock mornings UTC.
	phoenix := c.now().In(c.loc)
	dayLocal := time.Date(phoenix.Year(), phoenix.Month(), phoenix.Day(), 0, 0, 0, 0, time.UTC)

	subreddit, err := c.Bot.Store.GetSubredditByExternalID(ctx, result.Post.Subreddit)
	if err != nil {
		return fmt.Errorf("failed to get subreddit %q: %w", result.Post.Subreddit, err)
	}

	existing, err := c.Bot.Store.GetRollingPost(ctx, result.ChannelID, subreddit.ID, dayLocal)
	if err != nil {
		return fmt.Errorf("failed to fetch rolling post: %w", err)
	}

	var (
		title   string
		summary string
	)

	if existing == nil {
		title, summary = c.freshNarrative(ctx, result)
	} else {
		title, summary = c.updateNarrative(ctx, existing, result)
	}

	rp := buildRollingPostRow(existing, result, ch, subreddit, dayLocal, title, summary)
	embed := buildDigestEmbed(rp, result, subreddit.ExternalID)

	if existing == nil {
		msg, sendErr := c.sender.ChannelMessageSendComplex(ch.ExternalID, &discordgo.MessageSend{
			Embeds: []*discordgo.MessageEmbed{embed},
		})
		if sendErr != nil {
			return fmt.Errorf("failed to send message: %w", sendErr)
		}
		rp.DiscordMessageID = msg.ID
	} else {
		edited, editErr := c.sender.ChannelMessageEditComplex(&discordgo.MessageEdit{
			Channel: ch.ExternalID,
			ID:      existing.DiscordMessageID,
			Embeds:  &[]*discordgo.MessageEmbed{embed},
		})
		if isMessageGone(editErr) {
			_ = level.Warn(ctx.Log()).Log(
				"msg", "rolling digest message missing, sending a fresh one",
				"channel", ch.ExternalID, "message_id", existing.DiscordMessageID,
			)
			msg, sendErr := c.sender.ChannelMessageSendComplex(ch.ExternalID, &discordgo.MessageSend{
				Embeds: []*discordgo.MessageEmbed{embed},
			})
			if sendErr != nil {
				return fmt.Errorf("fallback send after edit-404 failed: %w", sendErr)
			}
			rp.DiscordMessageID = msg.ID
		} else if editErr != nil {
			return fmt.Errorf("failed to edit rolling digest: %w", editErr)
		} else {
			rp.DiscordMessageID = edited.ID
		}
	}

	if rp.DiscordMessageID == "" {
		// Defensive: every reachable branch above populates DiscordMessageID.
		// If this fires, it's a structural bug in SendMessage, not a runtime
		// failure we can recover from — surface it loudly rather than insert
		// an empty string and trip the schema CHECK constraint on the next edit.
		return fmt.Errorf("refusing to upsert rolling_posts with empty discord_message_id (channel=%d, sub=%d, day=%s)",
			rp.ChannelID, rp.SubredditID, rp.DayLocal.Format("2006-01-02"))
	}
	if _, err := c.Bot.Store.UpsertRollingPost(ctx, rp); err != nil {
		return fmt.Errorf("failed to upsert rolling post: %w", err)
	}
	if _, err := c.Bot.Store.InsertNotification(ctx, result.PostID, result.ChannelID, result.RuleID); err != nil {
		return fmt.Errorf("failed to insert notification into database: %w", err)
	}
	return nil
}

// freshNarrative tries the LLM; on any failure it falls back to the raw
// truncated selftext so matches are never silently dropped.
func (c *Client) freshNarrative(ctx ctxpkg.Ctx, result *evaluator.MatchingEvaluationResult) (string, string) {
	if c.shaper == nil {
		return result.Post.Title, rawSelftext(result.Post.Selftext)
	}
	out, err := c.shaper.ShapeFresh(ctx, llm.FreshInput{
		Post:         result.Post,
		RuleID:       result.RuleID,
		RuleTargetID: result.Rule.TargetID,
		RuleExact:    result.Rule.Exact,
	})
	if err != nil {
		_ = level.Warn(ctx.Log()).Log("msg", "llm fresh shape failed; falling back to raw", "error", err)
		return result.Post.Title, rawSelftext(result.Post.Selftext)
	}
	return out.Title, out.Summary
}

// updateNarrative tries the LLM Update mode; on failure it preserves the
// prior narrative unchanged so a transient vLLM hiccup doesn't clobber the
// existing digest body.
func (c *Client) updateNarrative(ctx ctxpkg.Ctx, existing *dbstore.RollingPost, result *evaluator.MatchingEvaluationResult) (string, string) {
	if c.shaper == nil {
		return existing.NarrativeTitle, existing.NarrativeSummary
	}
	out, err := c.shaper.ShapeUpdate(ctx, llm.UpdateInput{
		PriorTitle:      existing.NarrativeTitle,
		PriorSummary:    existing.NarrativeSummary,
		PriorPostCount:  len(existing.IncludedPostIDs),
		NewPost:         result.Post,
		NewRuleID:       result.RuleID,
		NewRuleTargetID: result.Rule.TargetID,
		NewRuleExact:    result.Rule.Exact,
	})
	if err != nil {
		_ = level.Warn(ctx.Log()).Log("msg", "llm update shape failed; keeping prior narrative", "error", err)
		return existing.NarrativeTitle, existing.NarrativeSummary
	}
	return out.Title, out.Summary
}

// buildRollingPostRow constructs the next rolling_posts row from the existing
// one (may be nil) plus the new match.
func buildRollingPostRow(
	existing *dbstore.RollingPost,
	result *evaluator.MatchingEvaluationResult,
	ch *dbstore.DiscordChannel,
	subreddit *dbstore.Subreddit,
	dayLocal time.Time,
	title, summary string,
) dbstore.RollingPost {
	_ = ch // ch.ID == result.ChannelID by construction; kept for clarity
	rp := dbstore.RollingPost{
		ChannelID:        result.ChannelID,
		SubredditID:      subreddit.ID,
		DayLocal:         dayLocal,
		NarrativeTitle:   title,
		NarrativeSummary: summary,
		LatestScore:      result.Post.Score,
		LatestComments:   result.Post.NumComments,
		LatestURL:        result.Post.URL,
		LatestThumbnail:  result.Post.Thumbnail,
	}
	if existing != nil {
		rp.DiscordMessageID = existing.DiscordMessageID
		rp.IncludedPostIDs = appendUnique(existing.IncludedPostIDs, result.Post.ID)
		rp.IncludedRuleIDs = appendUniqueInt(existing.IncludedRuleIDs, result.RuleID)
	} else {
		rp.IncludedPostIDs = []string{result.Post.ID}
		rp.IncludedRuleIDs = []int{result.RuleID}
	}
	return rp
}

// buildDigestEmbed renders a rolling_posts row into a discordgo embed.
func buildDigestEmbed(rp dbstore.RollingPost, result *evaluator.MatchingEvaluationResult, subredditExternalID string) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{
		URL:         rp.LatestURL,
		Type:        discordgo.EmbedTypeLink,
		Title:       truncateUTF8(rp.NarrativeTitle, 256),
		Description: truncateUTF8(rp.NarrativeSummary, 4000),
		Color:       embedColorReddit,
		Author: &discordgo.MessageEmbedAuthor{
			Name: fmt.Sprintf("r/%s", subredditExternalID),
		},
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Posts", Value: fmt.Sprintf("%d", len(rp.IncludedPostIDs)), Inline: true},
			{Name: "Score", Value: fmt.Sprintf("%d", rp.LatestScore), Inline: true},
			{Name: "Comments", Value: fmt.Sprintf("%d", rp.LatestComments), Inline: true},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: buildFooter(rp),
		},
	}
	if result.Post.CreatedUTC > 0 {
		embed.Timestamp = time.Unix(int64(result.Post.CreatedUTC), 0).UTC().Format(time.RFC3339)
	}
	if u, err := url.ParseRequestURI(rp.LatestThumbnail); err == nil {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: u.String()}
	}
	return embed
}

func buildFooter(rp dbstore.RollingPost) string {
	return fmt.Sprintf("%d matches • rules: %s • %s",
		len(rp.IncludedPostIDs),
		formatRuleIDs(rp.IncludedRuleIDs),
		rp.DayLocal.Format("2006-01-02"),
	)
}

func formatRuleIDs(ids []int) string {
	if len(ids) == 0 {
		return "—"
	}
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("#%d", id)
	}
	return out
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func appendUniqueInt(list []int, v int) []int {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// isMessageGone returns true if the Discord REST error indicates the target
// message has been deleted — i.e. sending a fresh one is the correct recovery.
//
// Deliberately NOT treating 403 as "gone": a 403 usually means the bot's
// channel permissions regressed, and the fallback send would fail with the
// same error, masking the real problem. Let 403s bubble up so they surface
// in logs as a real failure.
func isMessageGone(err error) bool {
	if err == nil {
		return false
	}
	var rest *discordgo.RESTError
	if errors.As(err, &rest) {
		if rest.Response != nil && rest.Response.StatusCode == 404 {
			return true
		}
		if rest.Message != nil && rest.Message.Code == discordgo.ErrCodeUnknownMessage {
			return true
		}
	}
	return false
}

// rawSelftext is the LLM-free fallback body. Same behaviour as pre-LLM reddit-spy.
func rawSelftext(s string) string {
	if s == "" {
		return "(no text)"
	}
	return truncateUTF8(s, 1024)
}

func truncateUTF8(s string, maxLen int) string {
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxLen])
}
