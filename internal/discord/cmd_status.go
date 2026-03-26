package discord

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/go-kit/log/level"
)

func (c *Client) statusCommandConfig() CommandConfig {
	return CommandConfig{
		Command: &discordgo.ApplicationCommand{
			Name:        "status",
			Description: "Show bot status and health information",
		},
		Handler: c.statusHandler,
	}
}

func (c *Client) statusHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	uptime := time.Since(c.Bot.StartedAt).Round(time.Second)
	pollerCount := c.Bot.PollerCount()
	latency := s.HeartbeatLatency().Round(time.Millisecond)

	embed := &discordgo.MessageEmbed{
		Title: "Reddit Spy - Status",
		Color: embedColorReddit,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Uptime",
				Value:  formatDuration(uptime),
				Inline: true,
			},
			{
				Name:   "Active Pollers",
				Value:  fmt.Sprintf("%d", pollerCount),
				Inline: true,
			},
			{
				Name:   "Latency",
				Value:  latency.String(),
				Inline: true,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Started at %s", c.Bot.StartedAt.UTC().Format(time.RFC822)),
		},
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:  discordgo.MessageFlagsEphemeral,
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	}); err != nil {
		_ = level.Error(c.Ctx.Log()).Log("error", "failed to send status response", "err", err)
	}
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
