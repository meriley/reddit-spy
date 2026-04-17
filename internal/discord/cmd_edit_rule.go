package discord

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/go-kit/log/level"

	database "github.com/meriley/reddit-spy/internal/dbstore"
)

func (c *Client) editRuleCommandConfig() CommandConfig {
	return CommandConfig{
		Command: &discordgo.ApplicationCommand{
			Name:        "edit_rule",
			Description: "Edit an existing rule's match value, exact/partial flag, or digest mode",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        "rule_id",
					Description: "The ID of the rule to edit (use /list_rules to find IDs)",
					Required:    true,
					Type:        discordgo.ApplicationCommandOptionInteger,
				},
				{
					Name:        "value",
					Description: "New match value (leave empty to keep current)",
					Required:    false,
					Type:        discordgo.ApplicationCommandOptionString,
				},
				{
					Name:        "exact",
					Description: "New exact/partial flag (leave empty to keep current)",
					Required:    false,
					Type:        discordgo.ApplicationCommandOptionBoolean,
				},
				{
					Name:        "digest_mode",
					Description: "New digest mode (leave empty to keep current)",
					Required:    false,
					Type:        discordgo.ApplicationCommandOptionString,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "narrative", Value: database.ModeNarrative},
						{Name: "music", Value: database.ModeMusic},
						{Name: "summary (TODO)", Value: database.ModeSummary},
						{Name: "media (TODO)", Value: database.ModeMedia},
					},
				},
				{
					Name:        "combine_hits_hours",
					Description: "New rolling-digest window in hours (leave empty to keep current)",
					Required:    false,
					Type:        discordgo.ApplicationCommandOptionInteger,
					MinValue:    ptrFloat(1),
					MaxValue:    720,
				},
			},
		},
		Handler: c.editRuleHandler,
	}
}

func (c *Client) editRuleHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !c.hasManageChannels(s, i) {
		c.respondWithError(s, i, "You need the **Manage Channels** permission to edit rules.")
		return
	}

	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		c.respondWithError(s, i, "rule_id is required")
		return
	}

	ruleIDFloat, ok := data.Options[0].Value.(float64)
	if !ok {
		c.respondWithError(s, i, "invalid rule_id value")
		return
	}
	ruleID := int(ruleIDFloat)

	rule, err := c.Bot.Store.GetRuleByID(c.Ctx, ruleID)
	if err != nil {
		c.respondWithError(s, i, fmt.Sprintf("Rule #%d not found.", ruleID))
		return
	}

	guild, err := c.Bot.Store.GetDiscordServerByExternalID(c.Ctx, i.GuildID)
	if err != nil || rule.ServerID != guild.ID {
		c.respondWithError(s, i, "You can only edit rules from this server.")
		return
	}

	newTarget := rule.Target
	newExact := rule.Exact
	newMode := rule.Mode
	if newMode == "" {
		newMode = database.ModeNarrative
	}
	newWindow := rule.WindowHours
	if newWindow <= 0 {
		newWindow = 72
	}

	for _, opt := range data.Options[1:] {
		switch opt.Name {
		case "value":
			if v, ok := opt.Value.(string); ok && v != "" {
				newTarget = v
			}
		case "exact":
			if v, ok := opt.Value.(bool); ok {
				newExact = v
			}
		case "digest_mode":
			if v, ok := opt.Value.(string); ok && v != "" {
				if !database.IsValidMode(v) {
					c.respondWithError(s, i, fmt.Sprintf("unknown digest mode %q", v))
					return
				}
				newMode = v
			}
		case "combine_hits_hours":
			if v, ok := opt.Value.(float64); ok && v > 0 {
				newWindow = int(v)
			}
		}
	}

	unchanged := newTarget == rule.Target &&
		newExact == rule.Exact &&
		newMode == rule.Mode &&
		newWindow == rule.WindowHours
	if unchanged {
		c.respondWithError(s, i, "No changes specified. Provide a new value, exact flag, digest mode, or combine_hits_hours.")
		return
	}

	if newTarget != rule.Target || newExact != rule.Exact {
		if err := c.Bot.Store.UpdateRule(c.Ctx, ruleID, newTarget, newExact); err != nil {
			_ = level.Error(c.Ctx.Log()).Log("error", "failed to update rule", "ruleID", ruleID, "err", err)
			c.respondWithError(s, i, "Failed to update rule.")
			return
		}
	}
	if newMode != rule.Mode {
		if err := c.Bot.Store.UpdateRuleMode(c.Ctx, ruleID, newMode); err != nil {
			_ = level.Error(c.Ctx.Log()).Log("error", "failed to update rule mode", "ruleID", ruleID, "err", err)
			c.respondWithError(s, i, "Failed to update rule digest mode.")
			return
		}
	}
	if newWindow != rule.WindowHours {
		if err := c.Bot.Store.UpdateRuleWindowHours(c.Ctx, ruleID, newWindow); err != nil {
			_ = level.Error(c.Ctx.Log()).Log("error", "failed to update rule window_hours", "ruleID", ruleID, "err", err)
			c.respondWithError(s, i, "Failed to update rule combine_hits_hours.")
			return
		}
	}

	matchType := "partial"
	if newExact {
		matchType = "exact"
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
			Content: fmt.Sprintf("Updated rule #%d: r/%s — %s %s match on `%s` · mode=%s · window=%dh",
				ruleID, rule.Subreddit, rule.TargetID, matchType, newTarget, newMode, newWindow),
		},
	})
}
