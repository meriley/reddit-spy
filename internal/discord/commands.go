package discord

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/go-kit/log/level"

	database "github.com/meriley/reddit-spy/internal/dbstore"
)

func (c *Client) addSubredditListenerCommandConfig() CommandConfig {
	return CommandConfig{
		Command: &discordgo.ApplicationCommand{
			Name:        "add_subreddit_listener",
			Description: "Add a listener to a subreddit to generate a message when criteria is met",
			Options:     c.subredditListenerOptions(),
		},
		Handler: c.subredditListenerHandler,
	}
}

func (c *Client) subredditListenerOptions() []*discordgo.ApplicationCommandOption {
	return []*discordgo.ApplicationCommandOption{
		{
			Name:        "subreddit",
			Description: "Name of the subreddit you want to listen to.",
			Required:    true,
			Type:        discordgo.ApplicationCommandOptionString,
		},
		{
			Name:        "match_on",
			Description: "Which value to you want to match on?",
			Required:    true,
			Type:        discordgo.ApplicationCommandOptionString,
			Choices: []*discordgo.ApplicationCommandOptionChoice{
				{Name: "author", Value: "author"},
				{Name: "title", Value: "title"},
			},
		},
		{
			Name:        "value",
			Description: "What value are you looking to match?",
			Required:    true,
			Type:        discordgo.ApplicationCommandOptionString,
		},
		{
			Name:        "exact",
			Description: "By setting this true, the match must an exact match, otherwise, the match may be partial.",
			Required:    true,
			Type:        discordgo.ApplicationCommandOptionBoolean,
		},
	}
}

func (c *Client) subredditListenerHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	var (
		rule        database.Rule
		subredditID string
	)

	for _, option := range data.Options {
		switch option.Name {
		case "subreddit":
			v, ok := option.Value.(string)
			if !ok {
				c.respondWithError(s, i, "invalid subreddit value")
				return
			}
			subredditID = strings.ToLower(v)
		case "match_on":
			v, ok := option.Value.(string)
			if !ok {
				c.respondWithError(s, i, "invalid match_on value")
				return
			}
			rule.TargetID = strings.ToLower(v)
		case "value":
			v, ok := option.Value.(string)
			if !ok {
				c.respondWithError(s, i, "invalid value")
				return
			}
			rule.Target = strings.ToLower(v)
		case "exact":
			v, ok := option.Value.(bool)
			if !ok {
				c.respondWithError(s, i, "invalid exact value")
				return
			}
			rule.Exact = v
		default:
			_ = level.Error(c.Ctx.Log()).Log("error", "unexpected key",
				"key", option.Name,
			)
		}
	}
	if err := c.Bot.CreateRule(c.Ctx, i.GuildID, i.ChannelID, subredditID, rule); err != nil {
		if irErr := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Flags:   discordgo.MessageFlagsEphemeral,
				Content: "Failed to create new subreddit listener",
			},
		}); irErr != nil {
			_ = level.Error(c.Ctx.Log()).
				Log("error", fmt.Errorf("failed to send interaction response on error: %w", irErr).Error(),
					"subreddit", rule.SubredditID,
					"serverID", rule.DiscordServerID,
					"channelID", rule.DiscordChannelID,
					"rule", fmt.Sprintf("%v", rule),
				)
			return
		}
		_ = level.Error(c.Ctx.Log()).Log("error", fmt.Errorf("failed to create rule: %w", err).Error(),
			"subreddit", rule.SubredditID,
			"serverID", rule.DiscordServerID,
			"channelID", rule.DiscordChannelID,
			"rule", fmt.Sprintf("%v", rule),
		)
		return
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "Rule Created Successfully!",
		},
	}); err != nil {
		_ = level.Error(c.Ctx.Log()).
			Log("error", fmt.Errorf("failed to send interaction response on success: %w", err).Error(),
				"subreddit", rule.SubredditID,
				"serverID", rule.DiscordServerID,
				"channelID", rule.DiscordChannelID,
				"rule", fmt.Sprintf("%v", rule),
			)
	}
}

func (c *Client) respondWithError(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	_ = level.Error(c.Ctx.Log()).Log("error", msg)
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: msg,
		},
	})
}
