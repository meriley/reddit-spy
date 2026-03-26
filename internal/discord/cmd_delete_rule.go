package discord

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/go-kit/log/level"
)

func (c *Client) deleteRuleCommandConfig() CommandConfig {
	return CommandConfig{
		Command: &discordgo.ApplicationCommand{
			Name:        "delete_rule",
			Description: "Delete a rule by its ID",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        "rule_id",
					Description: "The ID of the rule to delete (use /list_rules to find IDs)",
					Required:    true,
					Type:        discordgo.ApplicationCommandOptionInteger,
				},
			},
		},
		Handler: c.deleteRuleHandler,
	}
}

func (c *Client) deleteRuleHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !c.hasManageChannels(s, i) {
		c.respondWithError(s, i, "You need the **Manage Channels** permission to delete rules.")
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
		c.respondWithError(s, i, "You can only delete rules from this server.")
		return
	}

	if err := c.Bot.Store.DeleteRule(c.Ctx, ruleID); err != nil {
		_ = level.Error(c.Ctx.Log()).Log("error", "failed to delete rule", "ruleID", ruleID, "err", err)
		c.respondWithError(s, i, "Failed to delete rule.")
		return
	}

	matchType := "partial"
	if rule.Exact {
		matchType = "exact"
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
			Content: fmt.Sprintf("Deleted rule #%d (r/%s — %s %s match on `%s`)",
				ruleID, rule.Subreddit, rule.TargetID, matchType, rule.Target),
		},
	})
}
