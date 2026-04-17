package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/go-kit/log/level"
	"github.com/joho/godotenv"

	ctxpkg "github.com/meriley/reddit-spy/internal/context"
	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	"github.com/meriley/reddit-spy/internal/discord"
	"github.com/meriley/reddit-spy/internal/evaluator"
	"github.com/meriley/reddit-spy/internal/lastfm"
	"github.com/meriley/reddit-spy/internal/llm"
	"github.com/meriley/reddit-spy/internal/piped"
	"github.com/meriley/reddit-spy/redditDiscordBot"
)

var version = "dev"

func main() {
	baseCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// godotenv runs before ctxpkg.New so LOG_LEVEL set in config/.env takes
	// effect on the logger created below.
	_ = godotenv.Load("config/.env")

	appCtx := ctxpkg.New(baseCtx)

	_ = level.Info(appCtx.Log()).Log("msg", "starting reddit-spy", "version", version)

	store, err := dbstore.New(appCtx)
	if err != nil {
		panic(fmt.Errorf("failed to create db: %w", err))
	}
	defer store.Close()

	if err := dbstore.Bootstrap(
		appCtx,
		store.Pool,
		os.Getenv(dbstore.EnvPostgresUser),
		os.Getenv(dbstore.EnvPostgresPassword),
		os.Getenv(dbstore.EnvPostgresDatabase),
	); err != nil {
		panic(fmt.Errorf("failed to bootstrap db: %w", err))
	}

	bot, err := redditDiscordBot.New(appCtx, store)
	if err != nil {
		panic(fmt.Errorf("failed to create bot: %w", err))
	}

	discordOpts := []discord.Option{}
	if shaper := newShaper(appCtx); shaper != nil {
		discordOpts = append(discordOpts, discord.WithShaper(shaper))
	}
	// Music-mode popularity sort: keyless Last.fm artist-page scrape with a
	// Postgres-backed cache. Always on — failures are soft and the digest
	// falls back to source order.
	discordOpts = append(discordOpts, discord.WithLastfm(lastfm.New(lastfm.DefaultTimeout)))
	// YouTube link per entry via Piped — enabled only when PIPED_BASE_URL
	// is configured (public instance or self-hosted). Failures soft-fall
	// back to plain titles.
	if pipedURL := os.Getenv("PIPED_BASE_URL"); pipedURL != "" {
		discordOpts = append(discordOpts, discord.WithPiped(piped.New(pipedURL, piped.DefaultTimeout)))
		_ = level.Info(appCtx.Log()).Log("msg", "piped enabled", "base_url", pipedURL)
	}

	discordClient, err := discord.New(appCtx, bot, discordOpts...)
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

// newShaper builds an LLM shaper from env vars. Returns nil (not an error) if
// LLM_BASE_URL / LLM_MODEL aren't configured — reddit-spy degrades to the
// raw-selftext behaviour rather than refusing to start.
func newShaper(ctx ctxpkg.Ctx) *llm.Shaper {
	cfg, err := llm.ConfigFromEnv()
	if err != nil {
		_ = level.Warn(ctx.Log()).Log("msg", "llm disabled", "reason", err.Error())
		return nil
	}
	client, err := llm.NewClient(cfg)
	if err != nil {
		_ = level.Warn(ctx.Log()).Log("msg", "llm client init failed; disabling llm", "error", err)
		return nil
	}
	_ = level.Info(ctx.Log()).Log("msg", "llm enabled", "base_url", cfg.BaseURL, "model", cfg.Model, "timeout", cfg.Timeout)
	return llm.NewShaper(client, cfg)
}
