package discord

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/go-kit/log/level"
)

func (c *Client) listRulesCommandConfig() CommandConfig {
	return CommandConfig{
		Command: &discordgo.ApplicationCommand{
			Name:        "list_rules",
			Description: "List all active rules in this channel",
		},
		Handler: c.listRulesHandler,
	}
}

func (c *Client) listRulesHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	rules, err := c.Bot.Store.GetRulesByChannel(c.Ctx, i.ChannelID)
	if err != nil {
		_ = level.Error(c.Ctx.Log()).Log("error", "failed to get rules", "err", err)
		c.respondWithError(s, i, "Failed to fetch rules for this channel.")
		return
	}

	if len(rules) == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Flags:   discordgo.MessageFlagsEphemeral,
				Content: "No rules found in this channel. Use `/add_subreddit_listener` to create one.",
			},
		})
		return
	}

	var lines []string
	for _, r := range rules {
		matchType := "partial"
		if r.Exact {
			matchType = "exact"
		}
		lines = append(lines, fmt.Sprintf("**#%d** — r/%s | %s %s match on `%s`",
			r.ID, r.Subreddit, r.TargetID, matchType, r.Target))
	}

	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("Active Rules (%d)", len(rules)),
		Description: strings.Join(lines, "\n"),
		Color:       embedColorReddit,
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Use /delete_rule <id> to remove a rule",
		},
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:  discordgo.MessageFlagsEphemeral,
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	}); err != nil {
		_ = level.Error(c.Ctx.Log()).Log("error", "failed to send list rules response", "err", err)
	}
}
