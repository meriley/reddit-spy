package redditJSON

import (
	"time"

	"github.com/go-kit/log/level"
	ctx "github.com/meriley/reddit-spy/internal/context"
	"github.com/meriley/reddit-spy/internal/reddit"
)

type PollerInterface interface {
	Start(c chan []*RedditPost)
	Stop()
}

type Poller struct {
	context   ctx.Ctx
	client    *reddit.SpoofClient
	subreddit string
	interval  time.Duration
	quit      chan struct{}
}

func NewPoller(c ctx.Ctx, client *reddit.SpoofClient, subreddit string, interval time.Duration) *Poller {
	return &Poller{
		context:   c,
		client:    client,
		subreddit: subreddit,
		interval:  interval,
		quit:      make(chan struct{}),
	}
}

func (r *Poller) Start(c chan []*RedditPost) {
	ticker := time.NewTicker(r.interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				posts, _, err := r.client.GetSubredditPosts(r.context, r.subreddit, "", 25)
				if err != nil {
					_ = level.Error(r.context.Log()).Log("error", err.Error())
					continue
				}
				result := make([]*RedditPost, 0, len(posts))
				for _, p := range posts {
					result = append(result, &RedditPost{
						Author:      p.Author,
						ID:          p.ID,
						Permalink:   p.Permalink,
						Selftext:    p.Selftext,
						Subreddit:   p.Subreddit,
						Thumbnail:   p.Thumbnail,
						Title:       p.Title,
						URL:         p.URL,
						Score:       p.Score,
						NumComments: p.NumComments,
						CreatedUTC:  p.CreatedUTC,
					})
				}
				c <- result
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
		Author      string  `json:"author"`
		ID          string  `json:"id"`
		Permalink   string  `json:"permalink"`
		Selftext    string  `json:"selftext"`
		Subreddit   string  `json:"subreddit"`
		Thumbnail   string  `json:"thumbnail"`
		Title       string  `json:"title"`
		URL         string  `json:"URL"`
		Score       int     `json:"score"`
		NumComments int     `json:"num_comments"`
		CreatedUTC  float64 `json:"created_utc"`
	}
)
