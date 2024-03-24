package database

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"time"

	ctx "github.com/meriley/reddit-spy/internal/context"
)

const (
	EnvPostgresURI      = "postgres.address"
	EnvPostgresDatabase = "postgres.database"
	EnvPostgresUser     = "postgres.user"
	EnvPostgresPassword = "postgres.password"
)

type Rule struct {
	Target           string `bson:"target,omitempty"`
	Exact            bool   `bson:"exact,omitempty"`
	TargetId         string `bson:"targetId,omitempty"`
	DiscordServerID  string
	SubredditID      string
	DiscordChannelID string
}

type Store interface {
	InsertRule(rule Rule) error
	GetRules(subreddit string) ([]Rule, error)
}

type PGXStore struct {
	*pgxpool.Pool
	Ctx ctx.Context
}

func New(ctx ctx.Context) (Store, error) {
	uri := os.Getenv(EnvPostgresURI)
	if uri == "" {
		return nil, errors.New("expected postgres address")
	}
	user := os.Getenv(EnvPostgresUser)
	if user == "" {
		return nil, errors.New("expected postgres user")
	}
	pass := os.Getenv(EnvPostgresPassword)
	if pass == "" {
		return nil, errors.New("expected postgres password")
	}
	db := os.Getenv(EnvPostgresDatabase)
	if db == "" {
		return nil, errors.New("expected postgres database")
	}
	connString := fmt.Sprintf("postgres://%s:%s@%s/%s", user, pass, uri, db)
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("unable to parse config: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx.Context(), config)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to database: %w", err)
	}

	return &PGXStore{Ctx: ctx, Pool: pool}, nil
}

func (db *PGXStore) InsertRule(rule Rule) error {
	ctx, cancel := context.WithTimeout(db.Ctx.Context(), 5*time.Second)
	defer cancel()

	sql := `INSERT INTO 
		rules (
		   target, 
		   target_id, 
		   exact, 
		   channel_id, 
		   subreddit_id
		) VALUES ($1, $2, $3, $4, $5)`

	_, err := db.Exec(ctx, sql, rule.Target, rule.TargetId, rule.Exact, rule.DiscordChannelID, rule.SubredditID)
	if err != nil {
		return fmt.Errorf("failed to insert data: %w", err)
	}

	return nil
}

func (db *PGXStore) GetRules(subreddit string) ([]Rule, error) {
	// Use a context with a timeout to avoid hanging queries
	ctx, cancel := context.WithTimeout(db.Ctx.Context(), 5*time.Second)
	defer cancel()

	sql := `
		SELECT 
		    target, 
		    target_id, 
		    exact,
		    ds.server_id,
		    dc.channel_id, 
		    sr.subreddit_id 
		FROM rules r
			JOIN subreddits sr ON r.subreddit_id = sr.subreddit_id
			JOIN discord_channels dc on r.channel_id = dc.id
			JOIN discord_servers ds on dc.server_id = ds.id
		WHERE r.subreddit_id = $1
	`

	rows, err := db.Query(ctx, sql, subreddit)
	defer rows.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to query data: %w", err)
	}

	var rules []Rule
	for rows.Next() {
		var r Rule
		if err := rows.Scan(
			&r.Target,
			&r.TargetId,
			&r.Exact,
			&r.DiscordServerID,
			&r.DiscordChannelID,
			&r.SubredditID,
		); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		rules = append(rules, r)
	}

	return rules, nil
}
