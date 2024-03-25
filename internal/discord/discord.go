package discord

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/meriley/reddit-spy/internal/context"
	"github.com/meriley/reddit-spy/internal/evaluator"
	"github.com/meriley/reddit-spy/redditDiscordBot"
	"github.com/pkg/errors"
	"net/url"
	"os"
)

type Client struct {
	Ctx    context.Ctx
	Client *discordgo.Session
	Bot    *redditDiscordBot.RedditDiscordBot
}

func New(ctx context.Ctx, bot *redditDiscordBot.RedditDiscordBot) (*Client, error) {
	dg, err := discordgo.New("Bot " + os.Getenv("discord.token"))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create discord session")
	}
	dg.Identify.Intents = discordgo.IntentsGuilds
	err = dg.Open()
	if err != nil {
		return nil, errors.Wrap(err, "error opening discord session")
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
			return errors.Wrap(err, "failed to create application command")
		}
		c.Client.AddHandler(cmdConfig.Handler)
	}

	return nil
}

func (c *Client) SendMessage(ctx context.Context, data *evaluator.MatchingEvaluationResult) error {
	count, err := c.Bot.Store.GetNotificationCount(ctx, data.Post.ID, data.ChannelID, data.RuleID)
	if err != nil {
		return fmt.Errorf("unable to gte notification count: %w", err)
	}
	if count > 1 {
		return nil
	}
	end := 1024
	if end > len(data.Post.Selftext) {
		end = len(data.Post.Selftext)
	}
	substring := data.Post.Selftext[:end]

	message := &discordgo.MessageSend{
		Embed: &discordgo.MessageEmbed{
			URL:   data.Post.URL,
			Type:  discordgo.EmbedTypeLink,
			Title: data.Post.Title,
			Author: &discordgo.MessageEmbedAuthor{
				Name: data.Post.Author,
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
	if u, err := url.ParseRequestURI(data.Post.Thumbnail); err == nil {
		message.Embed.Thumbnail = &discordgo.MessageEmbedThumbnail{
			URL: u.String(),
		}
	}

	_, err = c.Client.ChannelMessageSendComplex(data.ChannelID, message)
	if err != nil {
		return errors.Wrap(err, "failed to send message")
	}
	if _, err := c.Bot.Store.InsertNotification(ctx, data.Post.ID, data.ChannelID, data.RuleID); err != nil {
		return errors.Wrap(err, "failed to insert notification into database")
	}
	return nil
}
