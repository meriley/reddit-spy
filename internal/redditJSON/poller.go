package redditJSON

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-kit/log/level"
	ctx "github.com/meriley/reddit-spy/internal/context"
)

type PollerInterface interface {
	Start(c chan []*RedditPost)
	Stop()
}

type Poller struct {
	context    ctx.Ctx
	httpClient *http.Client
	url        string
	interval   time.Duration
	quit       chan struct{}
}

func NewPoller(ctx ctx.Ctx, url string, interval time.Duration, timeout time.Duration) *Poller {
	return &Poller{
		context:    ctx,
		url:        url,
		httpClient: &http.Client{Timeout: timeout},
		interval:   interval,
		quit:       make(chan struct{}),
	}
}

func (r *Poller) Start(c chan []*RedditPost) {
	ticker := time.NewTicker(r.interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				feed, err := r.getJSONEntries(r.url)
				if err != nil {
					_ = level.Error(r.context.Log()).Log("error", err.Error())
					continue
				}
				c <- feed
			case <-r.quit:
				ticker.Stop()
				return
			}
		}
	}()
}

func (r *Poller) Stop() {
	close(r.quit)
}

type (
	RedditPost struct {
		Author    string `json:"author"`
		ID        string `json:"id"`
		Permalink string `json:"permalink"`
		Selftext  string `json:"selftext"`
		Subreddit string `json:"subreddit"`
		Thumbnail string `json:"thumbnail"`
		Title     string `json:"title"`
		URL       string `json:"URL"`
	}
	JSONEntryDataChildren struct {
		Data *RedditPost `json:"data"`
	}
	JSONEntryData struct {
		Children []*JSONEntryDataChildren `json:"children"`
	}
	JSONEntry struct {
		Data JSONEntryData `json:"data"`
	}
)

func (r *Poller) getJSONEntries(url string) ([]*RedditPost, error) {
	req, err := http.NewRequestWithContext(r.context, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("User-Agent", "reddit-spy/2.0 (github.com/meriley/reddit-spy)")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status %d from %s", resp.StatusCode, url)
	}

	var entries JSONEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("failed to decode json: %w", err)
	}

	posts := make([]*RedditPost, 0, len(entries.Data.Children))
	for _, child := range entries.Data.Children {
		posts = append(posts, child.Data)
	}

	return posts, nil
}
