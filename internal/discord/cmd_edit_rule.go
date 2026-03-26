package discord

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/go-kit/log/level"
)

func (c *Client) editRuleCommandConfig() CommandConfig {
	return CommandConfig{
		Command: &discordgo.ApplicationCommand{
			Name:        "edit_rule",
			Description: "Edit an existing rule's match value or exact/partial mode",
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
					Description: "New exact/partial mode (leave empty to keep current)",
					Required:    false,
					Type:        discordgo.ApplicationCommandOptionBoolean,
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
		}
	}

	if newTarget == rule.Target && newExact == rule.Exact {
		c.respondWithError(s, i, "No changes specified. Provide a new value or exact mode.")
		return
	}

	if err := c.Bot.Store.UpdateRule(c.Ctx, ruleID, newTarget, newExact); err != nil {
		_ = level.Error(c.Ctx.Log()).Log("error", "failed to update rule", "ruleID", ruleID, "err", err)
		c.respondWithError(s, i, "Failed to update rule.")
		return
	}

	matchType := "partial"
	if newExact {
		matchType = "exact"
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
			Content: fmt.Sprintf("Updated rule #%d: r/%s — %s %s match on `%s`",
				ruleID, rule.Subreddit, rule.TargetID, matchType, newTarget),
		},
	})
}
