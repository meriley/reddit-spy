package discord

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/go-kit/log/level"
	"github.com/meriley/reddit-spy/internal/database"
	"github.com/pkg/errors"
	"strings"
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
	rule := &database.Rule{}
	var subreddit string
	for _, option := range data.Options {
		switch option.Name {
		case "subreddit":
			subreddit = strings.ToLower(option.Value.(string))
		case "match_on":
			rule.TargetId = strings.ToLower(option.Value.(string))
		case "value":
			rule.Target = strings.ToLower(option.Value.(string))
		case "exact":
			rule.Exact = option.Value.(bool)
		default:
			level.Error(c.Ctx.Log()).Log("error", "unexpected key",
				"key", option.Name,
			)
		}
	}
	if err := c.Bot.InsertRule(subreddit, i.GuildID, i.ChannelID, rule); err != nil {
		if err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Flags:   discordgo.MessageFlagsEphemeral,
				Content: "Failed to create new subreddit listener",
			},
		}); err != nil {
			level.Error(c.Ctx.Log()).Log("error", errors.Wrap(err, "failed to send interaction response on error").Error(),
				"subreddit", subreddit,
				"serverID", i.GuildID,
				"channelID", i.ChannelID,
				"rule", fmt.Sprintf("%v", rule),
			)
			return
		}
		level.Error(c.Ctx.Log()).Log("error", errors.Wrap(err, "failed to create rule").Error(),
			"subreddit", subreddit,
			"serverID", i.GuildID,
			"channelID", i.ChannelID,
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
		level.Error(c.Ctx.Log()).Log("error", errors.Wrap(err, "failed to send interaction response on success").Error(),
			"subreddit", subreddit,
			"serverID", i.GuildID,
			"channelID", i.ChannelID,
			"rule", fmt.Sprintf("%v", rule),
		)
	}
}
