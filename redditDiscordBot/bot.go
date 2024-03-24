package redditDiscordBot

import (
	"fmt"
	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	"time"

	"github.com/meriley/reddit-spy/internal/context"
	"github.com/meriley/reddit-spy/internal/redditJSON"

	"github.com/pkg/errors"
)

type RedditDiscordBot struct {
	Ctx                   context.Ctx
	Store                 dbstore.Store
	Pollers               map[string]*redditJSON.Poller
	PollerResponseChannel chan []*redditJSON.RedditPost
}

func (b *RedditDiscordBot) AddSubredditPoller(
	subreddit string,
) *redditJSON.Poller {
	if poller, found := b.Pollers[subreddit]; found {
		return poller
	}
	poller := redditJSON.NewPoller(
		b.Ctx,
		fmt.Sprintf("https://www.reddit.com/r/%s/.json", subreddit),
		30*time.Second,
		5*time.Second,
	)
	poller.Start(b.PollerResponseChannel)
	return poller
}

func (b *RedditDiscordBot) InsertRule(
	rule dbstore.Rule,
) error {
	if err := b.Store.InsertRule(rule); err != nil {
		return errors.Wrap(err, "failed to insert rule")
	}
	b.AddSubredditPoller(rule.SubredditID)
	return nil
}

func New(ctx context.Ctx, store dbstore.Store) (*RedditDiscordBot, error) {
	return &RedditDiscordBot{
		Ctx:                   ctx,
		Store:                 store,
		Pollers:               make(map[string]*redditJSON.Poller),
		PollerResponseChannel: make(chan []*redditJSON.RedditPost, 10),
	}, nil
}
