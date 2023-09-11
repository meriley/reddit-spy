package redditDiscordBot

import (
	"github.com/meriley/reddit-spy/internal/evaluator"
	redditJson "github.com/meriley/reddit-spy/internal/redditJSON"
)

type BotInterface interface {
	AddSubredditPoller(subreddit string, responseChan chan []*redditJson.JSONEntryDataChildrenData) *redditJson.Poller
	SendMessage(data *evaluator.MatchingEvaluationResult) error
}
