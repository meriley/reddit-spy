package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-kit/log/level"
	"github.com/joho/godotenv"
	ctx "github.com/meriley/reddit-spy/internal/context"
	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	"github.com/meriley/reddit-spy/internal/discord"
	"github.com/meriley/reddit-spy/internal/evaluator"
	"github.com/meriley/reddit-spy/redditDiscordBot"
)

func main() {
	ctx := ctx.New(context.Background())

	err := godotenv.Load("config/.env")
	if err != nil {
		_ = level.Warn(ctx.Log()).Log("message", "No .env file found or error loading .env file, proceeding without it")
	}

	store, err := dbstore.New(ctx)
	if err != nil {
		panic(fmt.Errorf("failed to create db: %w", err))
	}
	bot, err := redditDiscordBot.New(ctx, store)
	if err != nil {
		panic(fmt.Errorf("failed to create bot: %w", err))
	}
	discordClient, err := discord.New(ctx, bot)
	if err != nil {
		panic(fmt.Errorf("failed to create discord client: %w", err))
	}
	// Get Subreddits to Listen
	subreddits, err := bot.Store.GetSubreddits(ctx)
	if err != nil {
		panic(fmt.Errorf("failed to get subreddits: %w", err))
	}
	_ = level.Info(ctx.Log()).Log("subreddits", fmt.Sprintf("%v", subreddits))

	// Start Polling For Reddit Posts
	for _, subreddit := range subreddits {
		bot.AddSubredditPoller(ctx, subreddit)
	}

	evaluate := evaluator.NewRuleEvaluator(store)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case posts := <-bot.PollerResponseChannel:
				if err := evaluate.Evaluate(ctx, posts, evaluate.EvaluateResponseChannel); err != nil {
					panic(fmt.Errorf("failed to evaluate rule: %w", err))
				}
			case result := <-evaluate.EvaluateResponseChannel:
				if err := discordClient.SendMessage(ctx, result); err != nil {
					panic(err)
				}
			case <-ctx.Done():
				close(bot.PollerResponseChannel)
				close(evaluate.EvaluateResponseChannel)
				_ = level.Info(ctx.Log()).Log("msg", "application terminated")
				return
			}
		}
	}()
	wg.Wait()
}
