package redditDiscordBot

import (
	"fmt"
	"github.com/meriley/reddit-spy/internal/context"
	"github.com/meriley/reddit-spy/internal/database"
	"github.com/meriley/reddit-spy/internal/redditJSON"
	"time"

	"github.com/pkg/errors"
)

type RedditDiscordBot struct {
	BotInterface
	Ctx                   context.Ctx
	DatabaseClient        *database.DB
	Pollers               map[string]*redditJSON.Poller
	PollerResponseChannel chan []*redditJSON.JSONEntryDataChildrenData
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
	subreddit string,
	serverID string,
	channelID string,
	rule *database.Rule,
) error {
	if err := b.DatabaseClient.InsertRule(subreddit, serverID, channelID, rule); err != nil {
		return errors.Wrap(err, "failed to insert rule")
	}
	b.AddSubredditPoller(subreddit)
	return nil
}

func New(ctx context.Ctx) (*RedditDiscordBot, error) {
	dbClient, err := database.New(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create database client")
	}
	return &RedditDiscordBot{
		Ctx:                   ctx,
		DatabaseClient:        dbClient,
		Pollers:               make(map[string]*redditJSON.Poller),
		PollerResponseChannel: make(chan []*redditJSON.JSONEntryDataChildrenData, 10),
	}, nil
}
