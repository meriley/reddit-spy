package discord

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/go-kit/log/level"
)

func (c *Client) pingCommandConfig() CommandConfig {
	return CommandConfig{
		Command: &discordgo.ApplicationCommand{
			Name:        "ping",
			Description: "Check bot latency",
		},
		Handler: c.pingHandler,
	}
}

func (c *Client) pingHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	latency := s.HeartbeatLatency().Round(time.Millisecond)

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: fmt.Sprintf("Pong! Latency: **%s**", latency),
		},
	}); err != nil {
		_ = level.Error(c.Ctx.Log()).Log("error", "failed to send ping response", "err", err)
	}
}
