package evaluator

import (
	"fmt"
	"strings"
	"sync"

	"github.com/pkg/errors"

	"github.com/meriley/reddit-spy/internal/context"
	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	redditJson "github.com/meriley/reddit-spy/internal/redditJSON"
)

type EvaluationInterface interface {
	Evaluate(posts []*redditJson.RedditPost) error
}

type RuleEvaluation struct {
	Ctx                     context.Ctx
	Store                   dbstore.Store
	EvaluateResponseChannel chan *MatchingEvaluationResult
}

type MatchingEvaluationResult struct {
	ServerID  string
	ChannelID string
	Post      *redditJson.RedditPost
}

func (e *RuleEvaluation) Evaluate(posts []*redditJson.RedditPost, resultChannel chan *MatchingEvaluationResult) error {
	for _, p := range posts {
		subreddit := p.Subreddit
		rules, err := e.Store.GetRules(subreddit)
		if err != nil {
			return fmt.Errorf("failed to fetch rules for %s: %w", subreddit, err)
		}

		var wg sync.WaitGroup
		wg.Add(len(rules))
		for _, r := range rules {
			go func(p *redditJson.RedditPost, r dbstore.Rule) {
				defer wg.Done()
				value, err := getValue(p, r)
				if err != nil {
					return
				}

				var result bool
				if r.Exact {
					result = evaluateExact(value, r.Target)
				} else {
					result = evaluatePartial(value, r.Target)
				}

				if result {
					resultChannel <- &MatchingEvaluationResult{
						ChannelID: r.DiscordChannelID,
						ServerID:  r.DiscordServerID,
						Post:      p,
					}
				}
			}(p, r)
		}
		wg.Wait()
	}
	return nil
}

func getValue(post *redditJson.RedditPost, rule dbstore.Rule) (string, error) {
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
	return strings.ToLower(value) == strings.ToLower(expected)
}

func evaluatePartial(value string, expected string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(expected))
}

func NewRuleEvaluator(ctx context.Ctx, store dbstore.Store) *RuleEvaluation {
	return &RuleEvaluation{
		Ctx:                     ctx,
		Store:                   store,
		EvaluateResponseChannel: make(chan *MatchingEvaluationResult, 10),
	}
}
