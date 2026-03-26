package discord

import (
	"fmt"
	"net/url"
	"os"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"

	"github.com/meriley/reddit-spy/internal/context"
	"github.com/meriley/reddit-spy/internal/evaluator"
	"github.com/meriley/reddit-spy/redditDiscordBot"
)

const embedColorReddit = 0xFF4500

type Client struct {
	Ctx    context.Ctx
	Client *discordgo.Session
	Bot    *redditDiscordBot.RedditDiscordBot
}

func New(ctx context.Ctx, bot *redditDiscordBot.RedditDiscordBot) (*Client, error) {
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

	client := &Client{
		Ctx:    ctx,
		Client: dg,
		Bot:    bot,
	}
	if err := client.RegisterCommands(); err != nil {
		return nil, err
	}

	return client, nil
}

func (c *Client) Close() error {
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
	}

	for _, cmdConfig := range commands {
		_, err := c.Client.ApplicationCommandCreate(c.Client.State.Application.ID, "", cmdConfig.Command)
		if err != nil {
			return fmt.Errorf("failed to create application command: %w", err)
		}
		c.Client.AddHandler(cmdConfig.Handler)
	}

	return nil
}

func (c *Client) SendMessage(ctx context.Ctx, result *evaluator.MatchingEvaluationResult) error {
	count, err := c.Bot.Store.GetNotificationCount(ctx, result.PostID, result.ChannelID, result.RuleID)
	if err != nil {
		return fmt.Errorf("unable to get notification count: %w", err)
	}
	if count > 0 {
		return nil
	}

	substring := truncateUTF8(result.Post.Selftext, 1024)
	if substring == "" {
		substring = "(no text)"
	}

	matchType := "partial"
	if result.Rule != nil && result.Rule.Exact {
		matchType = "exact"
	}
	footerText := fmt.Sprintf("Rule #%d | %s %s match", result.RuleID, result.Rule.TargetID, matchType)

	embed := &discordgo.MessageEmbed{
		URL:   result.Post.URL,
		Type:  discordgo.EmbedTypeLink,
		Title: result.Post.Title,
		Color: embedColorReddit,
		Author: &discordgo.MessageEmbedAuthor{
			Name: fmt.Sprintf("u/%s in r/%s", result.Post.Author, result.Post.Subreddit),
		},
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Summary",
				Value:  substring,
				Inline: false,
			},
			{
				Name:   "Score",
				Value:  fmt.Sprintf("%d", result.Post.Score),
				Inline: true,
			},
			{
				Name:   "Comments",
				Value:  fmt.Sprintf("%d", result.Post.NumComments),
				Inline: true,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: footerText,
		},
	}

	if result.Post.CreatedUTC > 0 {
		embed.Timestamp = time.Unix(int64(result.Post.CreatedUTC), 0).UTC().Format(time.RFC3339)
	}

	if u, err := url.ParseRequestURI(result.Post.Thumbnail); err == nil {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{
			URL: u.String(),
		}
	}

	message := &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
	}

	ch, err := c.Bot.Store.GetDiscordChannel(ctx, result.ChannelID)
	if err != nil {
		return fmt.Errorf("failed to get discord channel for id %d: %w", result.ChannelID, err)
	}
	_, err = c.Client.ChannelMessageSendComplex(ch.ExternalID, message)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	if _, err := c.Bot.Store.InsertNotification(ctx, result.PostID, result.ChannelID, result.RuleID); err != nil {
		return fmt.Errorf("failed to insert notification into database: %w", err)
	}
	return nil
}

func truncateUTF8(s string, maxLen int) string {
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxLen])
}
