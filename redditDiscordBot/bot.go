package redditDiscordBot

import (
	"context"
	"fmt"
	"time"

	ctx "github.com/meriley/reddit-spy/internal/context"
	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	"github.com/meriley/reddit-spy/internal/redditJSON"
)

type RedditDiscordBot struct {
	Ctx                   ctx.Context
	Store                 dbstore.Store
	Pollers               map[int]*redditJSON.Poller
	PollerResponseChannel chan []*redditJSON.RedditPost
}

func (b *RedditDiscordBot) AddSubredditPoller(
	ctx ctx.Context,
	subreddit *dbstore.Subreddit,
) *redditJSON.Poller {
	if poller, found := b.Pollers[subreddit.ID]; found {
		return poller
	}
	poller := redditJSON.NewPoller(
		ctx,
		fmt.Sprintf("https://www.reddit.com/r/%s/.json", subreddit.ExternalID),
		30*time.Second,
		5*time.Second,
	)
	b.Pollers[subreddit.ID] = poller
	poller.Start(b.PollerResponseChannel)
	return poller
}

func (b *RedditDiscordBot) CreateRule(
	ctx context.Context,
	serverID string,
	channelID string,
	subredditID string,
	rule dbstore.Rule,
) error {
	s, err := b.Store.InsertDiscordServer(ctx, serverID)
	if err != nil {
		return fmt.Errorf("failed to insert discord server: %w", err)
	}
	c, err := b.Store.InsertDiscordChannel(ctx, channelID, s.ID)
	if err != nil {
		return fmt.Errorf("failed to insert discord server: %w", err)
	}
	sr, err := b.Store.InsertSubreddit(ctx, subredditID)
	if err != nil {
		return fmt.Errorf("failed to insert discord server: %w", err)
	}
	rule.DiscordServerID = s.ID
	rule.DiscordChannelID = c.ID
	rule.SubredditID = sr.ID
	if _, err := b.Store.InsertRule(ctx, rule); err != nil {
		return fmt.Errorf("failed to insert rule: %w", err)
	}
	b.AddSubredditPoller(b.Ctx, sr)
	return nil
}

func New(ctx ctx.Context, store dbstore.Store) (*RedditDiscordBot, error) {
	return &RedditDiscordBot{
		Ctx:                   ctx,
		Store:                 store,
		Pollers:               make(map[int]*redditJSON.Poller),
		PollerResponseChannel: make(chan []*redditJSON.RedditPost, 10),
	}, nil
}
