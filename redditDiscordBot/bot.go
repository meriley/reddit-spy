package redditDiscordBot

import (
	"context"
	"fmt"
	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	"time"

	ctx "github.com/meriley/reddit-spy/internal/context"
	"github.com/meriley/reddit-spy/internal/redditJSON"

	"github.com/pkg/errors"
)

type RedditDiscordBot struct {
	Ctx                   ctx.Context
	Store                 dbstore.Store
	Pollers               map[int]*redditJSON.Poller
	PollerResponseChannel chan []*redditJSON.RedditPost
}

func (b *RedditDiscordBot) AddSubredditPoller(
	ctx ctx.Context,
	subreddit int,
) *redditJSON.Poller {
	if poller, found := b.Pollers[subreddit]; found {
		return poller
	}
	poller := redditJSON.NewPoller(
		ctx,
		fmt.Sprintf("https://www.reddit.com/r/%s/.json", subreddit),
		30*time.Second,
		5*time.Second,
	)
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
	sID, err := b.Store.InsertDiscordServer(ctx, serverID)
	if err != nil {
		return errors.Wrap(err, "failed to insert discord server")
	}
	cID, err := b.Store.InsertDiscordChannel(ctx, channelID, sID)
	if err != nil {
		return errors.Wrap(err, "failed to insert discord server")
	}
	srID, err := b.Store.InsertSubreddit(ctx, subredditID)
	if err != nil {
		return errors.Wrap(err, "failed to insert discord server")
	}
	rule.DiscordServerID = sID
	rule.DiscordChannelID = cID
	rule.SubredditID = srID
	if _, err := b.Store.InsertRule(ctx, rule); err != nil {
		return errors.Wrap(err, "failed to insert rule")
	}
	b.AddSubredditPoller(b.Ctx, srID)
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
