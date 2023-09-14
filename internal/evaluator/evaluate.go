package evaluator

import (
	"github.com/meriley/reddit-spy/internal/database"
	"github.com/pkg/errors"
	"strings"
	"sync"

	"github.com/meriley/reddit-spy/internal/context"
	redditJson "github.com/meriley/reddit-spy/internal/redditJSON"
)

type EvaluationInterface interface {
	Evaluate(posts []*redditJson.JSONEntryDataChildrenData) error
}

type RuleEvaluation struct {
	Ctx                     context.Ctx
	DB                      *database.DB
	EvaluateResponseChannel chan *MatchingEvaluationResult
}

type MatchingEvaluationResult struct {
	ServerID  string
	ChannelID string
	Post      *redditJson.JSONEntryDataChildrenData
}

func (e *RuleEvaluation) Evaluate(posts []*redditJson.JSONEntryDataChildrenData, resultChannel chan *MatchingEvaluationResult) error {
	var rulesDocument *database.RulesDocument
	for _, post := range posts {
		if rulesDocument == nil {
			var err error
			rulesDocument, err = e.DB.GetRulesDocument(post.Subreddit)
			if err != nil {
				return errors.Wrap(err, "failed to get rules document")
			}
		}

		for serverID, serverValues := range rulesDocument.Servers {
			for channelID, channelValues := range serverValues.Channels {
				var wg sync.WaitGroup
				wg.Add(len(channelValues.Rules))
				for _, rule := range channelValues.Rules {
					sid := serverID
					cid := channelID
					p := post
					r := rule
					go func() {
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
								ChannelID: cid,
								ServerID:  sid,
								Post:      p,
							}
						}
					}()
				}
				wg.Wait()
			}
		}
	}
	return nil
}

func getValue(post *redditJson.JSONEntryDataChildrenData, rule *database.Rule) (string, error) {
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

func NewRuleEvaluator(ctx context.Ctx, db *database.DB) *RuleEvaluation {
	return &RuleEvaluation{
		Ctx:                     ctx,
		DB:                      db,
		EvaluateResponseChannel: make(chan *MatchingEvaluationResult, 10),
	}
}
