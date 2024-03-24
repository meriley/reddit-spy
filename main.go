package main

import (
	"fmt"
	"github.com/go-kit/log/level"
	"github.com/joho/godotenv"
	"github.com/meriley/reddit-spy/internal/context"
	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	"github.com/meriley/reddit-spy/internal/discord"
	"github.com/meriley/reddit-spy/internal/evaluator"
	"github.com/meriley/reddit-spy/redditDiscordBot"
	"github.com/pkg/errors"
	"sync"
)

func main() {
	err := godotenv.Load("config/.env")
	if err != nil {
		panic(errors.Wrap(err, "Error loading .env file"))
	}

	ctx := context.NewContext()
	store, err := dbstore.New(ctx)
	if err != nil {
		panic(errors.Wrap(err, "failed to create db store"))
	}
	bot, err := redditDiscordBot.New(ctx, store)
	if err != nil {
		panic(errors.Wrap(err, "failed to create bot"))
	}
	discordClient, err := discord.New(ctx, bot)
	if err != nil {
		panic(errors.Wrap(err, "failed to create discord client"))
	}
	// Get Subreddits to Listen
	subreddits, err := bot.Store.GetSubreddits()
	if err != nil {
		panic(errors.Wrap(err, "failed to get subreddits"))
	}
	level.Info(ctx.Log()).Log("subreddits", fmt.Sprintf("%s", subreddits))

	// Start Polling For Reddit Posts
	for _, subreddit := range subreddits {
		bot.AddSubredditPoller(subreddit)
	}

	evaluate := evaluator.NewRuleEvaluator(ctx, bot.Store)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case posts := <-bot.PollerResponseChannel:
				if err := evaluate.Evaluate(posts, evaluate.EvaluateResponseChannel); err != nil {
					panic(errors.Wrap(err, "failed to evaluate rule"))
				}
			case result := <-evaluate.EvaluateResponseChannel:
				if err := discordClient.SendMessage(result); err != nil {
					panic(err)
				}
			case <-ctx.Done:
				close(bot.PollerResponseChannel)
				close(evaluate.EvaluateResponseChannel)
				level.Info(ctx.Log()).Log("msg", "application terminated")
				return
			}
		}
	}()
	wg.Wait()
}
