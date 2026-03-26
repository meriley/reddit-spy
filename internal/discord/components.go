package discord

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/go-kit/log/level"
)

func (c *Client) componentHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	customID := i.MessageComponentData().CustomID

	if strings.HasPrefix(customID, "delete_rule:") {
		c.handleDeleteRuleButton(s, i, customID)
		return
	}

	_ = level.Warn(c.Ctx.Log()).Log("msg", "unknown component interaction", "customID", customID)
}

func (c *Client) handleDeleteRuleButton(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) {
	if !c.hasManageChannels(s, i) {
		c.respondComponentError(s, i, "You need the **Manage Channels** permission to delete rules.")
		return
	}

	idStr := strings.TrimPrefix(customID, "delete_rule:")
	ruleID, err := strconv.Atoi(idStr)
	if err != nil {
		c.respondComponentError(s, i, "Invalid rule ID.")
		return
	}

	rule, err := c.Bot.Store.GetRuleByID(c.Ctx, ruleID)
	if err != nil {
		c.respondComponentError(s, i, fmt.Sprintf("Rule #%d not found.", ruleID))
		return
	}

	guild, err := c.Bot.Store.GetDiscordServerByExternalID(c.Ctx, i.GuildID)
	if err != nil || rule.ServerID != guild.ID {
		c.respondComponentError(s, i, "You can only delete rules from this server.")
		return
	}

	if err := c.Bot.Store.DeleteRule(c.Ctx, ruleID); err != nil {
		_ = level.Error(c.Ctx.Log()).Log("error", "failed to delete rule via button", "ruleID", ruleID, "err", err)
		c.respondComponentError(s, i, "Failed to delete rule.")
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

func (c *Client) respondComponentError(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: msg,
		},
	})
}
