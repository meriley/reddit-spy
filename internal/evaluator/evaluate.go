package evaluator

import (
	"errors"
	"fmt"
	"strings"

	ctx "github.com/meriley/reddit-spy/internal/context"

	"golang.org/x/sync/errgroup"

	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	redditJson "github.com/meriley/reddit-spy/internal/redditJSON"
)

type RuleEvaluation struct {
	store                   dbstore.Store
	EvaluateResponseChannel chan *MatchingEvaluationResult
}

type MatchingEvaluationResult struct {
	ChannelID int
	RuleID    int
	PostID    int
	Post      *redditJson.RedditPost
}

func (e *RuleEvaluation) Evaluate(
	ctx ctx.Ctx,
	posts []*redditJson.RedditPost,
	resultChannel chan *MatchingEvaluationResult,
) error {
	// Deduplicate subreddit lookups: all posts in a batch share the same subreddit
	subredditCache := make(map[string]*dbstore.Subreddit)
	rulesCache := make(map[int][]*dbstore.Rule)

	for _, p := range posts {
		subredditName := p.Subreddit

		subreddit, ok := subredditCache[subredditName]
		if !ok {
			var err error
			subreddit, err = e.store.GetSubredditByExternalID(ctx, subredditName)
			if err != nil {
				return fmt.Errorf("failed to get subreddit %s: %w", subredditName, err)
			}
			subredditCache[subredditName] = subreddit
		}

		rules, ok := rulesCache[subreddit.ID]
		if !ok {
			var err error
			rules, err = e.store.GetRules(ctx, subreddit.ID)
			if err != nil {
				return fmt.Errorf("failed to fetch rules for %s: %w", subredditName, err)
			}
			rulesCache[subreddit.ID] = rules
		}

		eg, egCtx := errgroup.WithContext(ctx)
		for _, r := range rules {
			p := p
			r := r
			eg.Go(func() error {
				value, err := getValue(p, r)
				if err != nil {
					return fmt.Errorf("failed to get value for post %s: %w", p.ID, err)
				}

				var result bool
				if r.Exact {
					result = evaluateExact(value, r.Target)
				} else {
					result = evaluatePartial(value, r.Target)
				}

				if result {
					dbP, err := e.store.InsertPost(egCtx, p.ID)
					if err != nil {
						return fmt.Errorf("failed to insert post to store: %w", err)
					}
					select {
					case resultChannel <- &MatchingEvaluationResult{
						ChannelID: r.DiscordChannelID,
						RuleID:    r.ID,
						PostID:    dbP.ID,
						Post:      p,
					}:
					case <-egCtx.Done():
						return egCtx.Err()
					}
				}
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return err
		}
	}
	return nil
}

func getValue(post *redditJson.RedditPost, rule *dbstore.Rule) (string, error) {
	switch rule.TargetID {
	case "author":
		return post.Author, nil
	case "title":
		return post.Title, nil
	default:
		return "", errors.New("unexpected target id")
	}
}

func evaluateExact(value string, expected string) bool {
	return strings.EqualFold(value, expected)
}

func evaluatePartial(value string, expected string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(expected))
}

func NewRuleEvaluator(store dbstore.Store) *RuleEvaluation {
	return &RuleEvaluation{
		store:                   store,
		EvaluateResponseChannel: make(chan *MatchingEvaluationResult, 10),
	}
}
