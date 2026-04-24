package redditDiscordBot

import (
	"context"
	"fmt"
	"sync"
	"time"

	ctx "github.com/meriley/reddit-spy/internal/context"
	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	"github.com/meriley/reddit-spy/internal/reddit"
	"github.com/meriley/reddit-spy/internal/redditJSON"
)

const (
	DefaultPollInterval = 30 * time.Second
	PollerChannelBuffer = 10
)

type RedditDiscordBot struct {
	ctx                   ctx.Ctx
	Store                 dbstore.Store
	Reddit                *reddit.SpoofClient
	StartedAt             time.Time
	mu                    sync.RWMutex
	pollers               map[int]*redditJSON.Poller
	PollerResponseChannel chan []*redditJSON.RedditPost
}

func (b *RedditDiscordBot) PollerCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.pollers)
}

func (b *RedditDiscordBot) AddSubredditPoller(
	c ctx.Ctx,
	subreddit *dbstore.Subreddit,
) *redditJSON.Poller {
	b.mu.Lock()
	defer b.mu.Unlock()

	if poller, found := b.pollers[subreddit.ID]; found {
		return poller
	}
	poller := redditJSON.NewPoller(
		c,
		b.Reddit,
		subreddit.ExternalID,
		DefaultPollInterval,
	)
	b.pollers[subreddit.ID] = poller
	poller.Start(b.PollerResponseChannel)
	return poller
}

func (b *RedditDiscordBot) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	for id, poller := range b.pollers {
		poller.Stop()
		delete(b.pollers, id)
	}
}

func (b *RedditDiscordBot) CreateRule(
	c ctx.Ctx,
	serverID string,
	channelID string,
	subredditID string,
	rule dbstore.Rule,
) error {
	s, err := b.Store.InsertDiscordServer(c, serverID)
	if err != nil {
		return fmt.Errorf("failed to insert discord server: %w", err)
	}
	ch, err := b.Store.InsertDiscordChannel(c, channelID, s.ID)
	if err != nil {
		return fmt.Errorf("failed to insert discord channel: %w", err)
	}
	sr, err := b.Store.InsertSubreddit(c, subredditID)
	if err != nil {
		return fmt.Errorf("failed to insert subreddit: %w", err)
	}
	rule.DiscordServerID = s.ID
	rule.DiscordChannelID = ch.ID
	rule.SubredditID = sr.ID
	if _, err := b.Store.InsertRule(c, rule); err != nil {
		return fmt.Errorf("failed to insert rule: %w", err)
	}
	b.AddSubredditPoller(b.ctx, sr)
	return nil
}

// ValidateSubredditExists checks whether the given subreddit is accessible.
// Returns false immediately for an empty name without making a network call.
func (b *RedditDiscordBot) ValidateSubredditExists(ctx context.Context, subreddit string) bool {
	if subreddit == "" {
		return false
	}
	_, _, err := b.Reddit.GetSubredditPosts(ctx, subreddit, "", 1)
	return err == nil
}

func New(c ctx.Ctx, store dbstore.Store) (*RedditDiscordBot, error) {
	return &RedditDiscordBot{
		ctx:                   c,
		Store:                 store,
		Reddit:                reddit.NewSpoofClient(reddit.SpoofConfig{}),
		StartedAt:             time.Now(),
		pollers:               make(map[int]*redditJSON.Poller),
		PollerResponseChannel: make(chan []*redditJSON.RedditPost, PollerChannelBuffer),
	}, nil
}
