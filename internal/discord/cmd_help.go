package discord

import (
	"github.com/bwmarrin/discordgo"
	"github.com/go-kit/log/level"
)

func (c *Client) helpCommandConfig() CommandConfig {
	return CommandConfig{
		Command: &discordgo.ApplicationCommand{
			Name:        "help",
			Description: "Show available commands and how to use reddit-spy",
		},
		Handler: c.helpHandler,
	}
}

func (c *Client) helpHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	embed := &discordgo.MessageEmbed{
		Title:       "Reddit Spy - Help",
		Description: "Monitor subreddits and get Discord notifications when posts match your rules.",
		Color:       embedColorReddit,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:  "/add_subreddit_listener",
				Value: "Create a rule to watch a subreddit for posts matching a keyword by author or title. Requires **Manage Channels**.",
			},
			{
				Name:  "/list_rules",
				Value: "List all active rules in the current channel.",
			},
			{
				Name:  "/edit_rule",
				Value: "Edit a rule's match value or exact/partial mode. Requires **Manage Channels**.",
			},
			{
				Name:  "/delete_rule",
				Value: "Delete a rule by its ID (use /list_rules to find IDs). Requires **Manage Channels**.",
			},
			{
				Name:  "/ping",
				Value: "Check bot latency.",
			},
			{
				Name:  "/status",
				Value: "Show bot uptime, active pollers, and health info.",
			},
			{
				Name:  "/help",
				Value: "Show this help message.",
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "github.com/meriley/reddit-spy",
		},
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:  discordgo.MessageFlagsEphemeral,
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	}); err != nil {
		_ = level.Error(c.Ctx.Log()).Log("error", "failed to send help response", "err", err)
	}
}
