package main

import (
	"context"
	"fmt"
	"os/signal"
	"sync"
	"syscall"

	"github.com/go-kit/log/level"
	"github.com/joho/godotenv"
	ctx "github.com/meriley/reddit-spy/internal/context"
	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	"github.com/meriley/reddit-spy/internal/discord"
	"github.com/meriley/reddit-spy/internal/evaluator"
	"github.com/meriley/reddit-spy/redditDiscordBot"
)

var version = "dev"

func main() {
	baseCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	appCtx := ctx.New(baseCtx)

	_ = level.Info(appCtx.Log()).Log("msg", "starting reddit-spy", "version", version)

	err := godotenv.Load("config/.env")
	if err != nil {
		_ = level.Warn(appCtx.Log()).Log("msg", "no .env file found, proceeding without it")
	}

	store, err := dbstore.New(appCtx)
	if err != nil {
		panic(fmt.Errorf("failed to create db: %w", err))
	}
	defer store.Close()

	bot, err := redditDiscordBot.New(appCtx, store)
	if err != nil {
		panic(fmt.Errorf("failed to create bot: %w", err))
	}

	discordClient, err := discord.New(appCtx, bot)
	if err != nil {
		panic(fmt.Errorf("failed to create discord client: %w", err))
	}
	defer discordClient.Close()

	subreddits, err := bot.Store.GetSubreddits(appCtx)
	if err != nil {
		panic(fmt.Errorf("failed to get subreddits: %w", err))
	}
	for _, sr := range subreddits {
		_ = level.Info(appCtx.Log()).Log("msg", "starting poller", "subreddit", sr.ExternalID)
	}

	for _, subreddit := range subreddits {
		bot.AddSubredditPoller(appCtx, subreddit)
	}

	evaluate := evaluator.NewRuleEvaluator(store)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case posts := <-bot.PollerResponseChannel:
				if err := evaluate.Evaluate(appCtx, posts, evaluate.EvaluateResponseChannel); err != nil {
					_ = level.Error(appCtx.Log()).Log("msg", "evaluate failed", "error", err)
				}
			case result := <-evaluate.EvaluateResponseChannel:
				if err := discordClient.SendMessage(appCtx, result); err != nil {
					_ = level.Error(appCtx.Log()).Log("msg", "send message failed", "error", err)
				}
			case <-appCtx.Done():
				_ = level.Info(appCtx.Log()).Log("msg", "shutting down")
				bot.Stop()
				_ = level.Info(appCtx.Log()).Log("msg", "application terminated")
				return
			}
		}
	}()
	wg.Wait()
}
