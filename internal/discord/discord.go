package discord

import (
	"fmt"
	"net/url"
	"os"

	"github.com/bwmarrin/discordgo"

	"github.com/meriley/reddit-spy/internal/context"
	"github.com/meriley/reddit-spy/internal/evaluator"
	"github.com/meriley/reddit-spy/redditDiscordBot"
)

type Client struct {
	Ctx    context.RedditSpyCtx
	Client *discordgo.Session
	Bot    *redditDiscordBot.RedditDiscordBot
}

func New(ctx context.RedditSpyCtx, bot *redditDiscordBot.RedditDiscordBot) (*Client, error) {
	dg, err := discordgo.New("Bot " + os.Getenv("discord.token"))
	if err != nil {
		return nil, fmt.Errorf("failed to create discord session: %w", err)
	}
	dg.Identify.Intents = discordgo.IntentsGuilds
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

type CommandConfig struct {
	Command *discordgo.ApplicationCommand
	Handler func(s *discordgo.Session, i *discordgo.InteractionCreate)
}

func (c *Client) RegisterCommands() error {
	commands := []CommandConfig{
		c.addSubredditListenerCommandConfig(),
		// Add more command configurations here as needed
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
	if count > 1 {
		return nil
	}
	end := 1024
	if end > len(result.Post.Selftext) {
		end = len(result.Post.Selftext)
	}
	substring := result.Post.Selftext[:end]

	message := &discordgo.MessageSend{
		Embed: &discordgo.MessageEmbed{
			URL:   result.Post.URL,
			Type:  discordgo.EmbedTypeLink,
			Title: result.Post.Title,
			Author: &discordgo.MessageEmbedAuthor{
				Name: result.Post.Author,
			},
			Fields: []*discordgo.MessageEmbedField{
				{
					Name:   "Summary",
					Value:  substring,
					Inline: true,
				},
			},
		},
	}
	if u, err := url.ParseRequestURI(result.Post.Thumbnail); err == nil {
		message.Embed.Thumbnail = &discordgo.MessageEmbedThumbnail{
			URL: u.String(),
		}
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
