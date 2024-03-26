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

type EvaluationInterface interface {
	Evaluate(posts []*redditJson.RedditPost) error
}

type RuleEvaluation struct {
	Store                   dbstore.Store
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
	for _, p := range posts {
		subredditID := p.Subreddit
		subreddit, err := e.Store.GetSubredditByExternalID(ctx, subredditID)
		if err != nil {
			return fmt.Errorf("failed to get subreddit %s: %w", subredditID, err)
		}
		rules, err := e.Store.GetRules(ctx, subreddit.ID)
		if err != nil {
			return fmt.Errorf("failed to fetch rules for %s: %w", subredditID, err)
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
					dbP, err := e.Store.InsertPost(egCtx, p.ID)
					if err != nil {
						return fmt.Errorf("failed to insert post to store: %w", err)
					}
					resultChannel <- &MatchingEvaluationResult{
						ChannelID: r.DiscordChannelID,
						RuleID:    r.ID,
						PostID:    dbP.ID,
						Post:      p,
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
	switch rule.TargetId {
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
		Store:                   store,
		EvaluateResponseChannel: make(chan *MatchingEvaluationResult, 10),
	}
}
