package redditDiscordBot

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	ctx "github.com/meriley/reddit-spy/internal/context"
	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	"github.com/meriley/reddit-spy/internal/redditJSON"
)

const (
	DefaultPollInterval = 30 * time.Second
	DefaultHTTPTimeout  = 5 * time.Second
	PollerChannelBuffer = 10
)

type RedditDiscordBot struct {
	ctx                   ctx.Ctx
	Store                 dbstore.Store
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
	ctx ctx.Ctx,
	subreddit *dbstore.Subreddit,
) *redditJSON.Poller {
	b.mu.Lock()
	defer b.mu.Unlock()

	if poller, found := b.pollers[subreddit.ID]; found {
		return poller
	}
	poller := redditJSON.NewPoller(
		ctx,
		fmt.Sprintf("https://www.reddit.com/r/%s/.json", subreddit.ExternalID),
		DefaultPollInterval,
		DefaultHTTPTimeout,
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
	ctx ctx.Ctx,
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
		return fmt.Errorf("failed to insert discord channel: %w", err)
	}
	sr, err := b.Store.InsertSubreddit(ctx, subredditID)
	if err != nil {
		return fmt.Errorf("failed to insert subreddit: %w", err)
	}
	rule.DiscordServerID = s.ID
	rule.DiscordChannelID = c.ID
	rule.SubredditID = sr.ID
	if _, err := b.Store.InsertRule(ctx, rule); err != nil {
		return fmt.Errorf("failed to insert rule: %w", err)
	}
	b.AddSubredditPoller(b.ctx, sr)
	return nil
}

func ValidateSubredditExists(subreddit string) bool {
	if subreddit == "" {
		return false
	}
	client := &http.Client{Timeout: DefaultHTTPTimeout}
	req, err := http.NewRequest(http.MethodHead, fmt.Sprintf("https://www.reddit.com/r/%s/.json", subreddit), nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "reddit-spy/2.0 (github.com/meriley/reddit-spy)")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func New(ctx ctx.Ctx, store dbstore.Store) (*RedditDiscordBot, error) {
	return &RedditDiscordBot{
		ctx:                   ctx,
		Store:                 store,
		StartedAt:             time.Now(),
		pollers:               make(map[int]*redditJSON.Poller),
		PollerResponseChannel: make(chan []*redditJSON.RedditPost, PollerChannelBuffer),
	}, nil
}
