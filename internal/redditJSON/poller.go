package redditJSON

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-kit/log/level"
	ctx "github.com/meriley/reddit-spy/internal/context"
)

const (
	MAX_PAGINATION = 102
)

var quit chan struct{}

type PollerInterface interface {
	Start(c chan *JSONEntry)
	Stop()
}

type Poller struct {
	PollerInterface
	Context    ctx.Ctx
	HttpClient *http.Client
	Url        string
	Timeout    time.Duration
	Interval   time.Duration
}

func NewPoller(ctx ctx.Ctx, url string, interval time.Duration, timeout time.Duration) *Poller {
	quit = make(chan struct{})
	return &Poller{
		Context:    ctx,
		Url:        url,
		HttpClient: &http.Client{Timeout: timeout},
		Interval:   interval,
		Timeout:    timeout,
	}
}

func (r *Poller) Start(c chan []*RedditPost) {
	ticker := time.NewTicker(r.Interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				feed, err := r.getJSONEntries(r.Url)
				if err != nil {
					_ = level.Error(r.Context.Log()).Log("error", err.Error())
				}
				c <- feed
			case <-quit:
				ticker.Stop()
				return
			}
		}
	}()
}

func (r *Poller) Stop() {
	close(quit)
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
	resp, err := r.HttpClient.Get(url)
	if err != nil || resp.StatusCode != 200 {
		return nil, err
	}
	defer resp.Body.Close()

	var entries JSONEntry
	err = json.NewDecoder(resp.Body).Decode(&entries)
	if err != nil {
		return nil, fmt.Errorf("failed to decode json :%w", err)
	}

	posts := make([]*RedditPost, 0, MAX_PAGINATION)
	for _, child := range entries.Data.Children {
		posts = append(posts, child.Data)
	}

	return posts, nil
}
