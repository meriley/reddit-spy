package discord

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/go-kit/log/level"

	database "github.com/meriley/reddit-spy/internal/dbstore"
	"github.com/meriley/reddit-spy/redditDiscordBot"
)

var subredditPattern = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

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
		{
			Name:        "mode",
			Description: "Digest style. Default: narrative. music = release extraction; summary/media are TODO.",
			Required:    false,
			Type:        discordgo.ApplicationCommandOptionString,
			Choices: []*discordgo.ApplicationCommandOptionChoice{
				{Name: "narrative (rewritten prose on each match)", Value: database.ModeNarrative},
				{Name: "music (extract releases, dedupe list)", Value: database.ModeMusic},
				{Name: "summary (TODO)", Value: database.ModeSummary},
				{Name: "media (TODO)", Value: database.ModeMedia},
			},
		},
	}
}

func (c *Client) subredditListenerHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !c.hasManageChannels(s, i) {
		c.respondWithError(s, i, "You need the **Manage Channels** permission to create rules.")
		return
	}

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
		case "mode":
			v, ok := option.Value.(string)
			if !ok {
				c.respondWithError(s, i, "invalid mode value")
				return
			}
			if !database.IsValidMode(v) {
				c.respondWithError(s, i, fmt.Sprintf("unknown mode %q", v))
				return
			}
			rule.Mode = v
		default:
			_ = level.Error(c.Ctx.Log()).Log("error", "unexpected key",
				"key", option.Name,
			)
		}
	}
	if subredditID == "" || !subredditPattern.MatchString(subredditID) {
		c.respondWithError(s, i, "Invalid subreddit name. Use only letters, numbers, and underscores.")
		return
	}
	if len(subredditID) > 21 {
		c.respondWithError(s, i, "Subreddit name is too long (max 21 characters).")
		return
	}
	if rule.Target == "" {
		c.respondWithError(s, i, "Match value cannot be empty.")
		return
	}

	if !redditDiscordBot.ValidateSubredditExists(subredditID) {
		c.respondWithError(s, i, fmt.Sprintf("Subreddit r/%s does not exist or is not accessible.", subredditID))
		return
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

func (c *Client) hasManageChannels(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if i.Member == nil {
		return false
	}
	perms := i.Member.Permissions
	return perms&discordgo.PermissionManageChannels != 0 || perms&discordgo.PermissionAdministrator != 0
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
