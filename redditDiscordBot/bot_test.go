package redditDiscordBot

import (
	"testing"

	"github.com/meriley/reddit-spy/internal/redditJSON"
)

func TestValidateSubredditExists_Empty(t *testing.T) {
	result := ValidateSubredditExists("")
	if result {
		t.Error("ValidateSubredditExists('') should return false")
	}
}

func TestPollerCount(t *testing.T) {
	bot := &RedditDiscordBot{
		pollers: make(map[int]*redditJSON.Poller),
	}

	if got := bot.PollerCount(); got != 0 {
		t.Errorf("PollerCount() = %d, want 0", got)
	}
}
