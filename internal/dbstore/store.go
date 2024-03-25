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
	ID               int
	Target           string `bson:"target,omitempty"`
	Exact            bool   `bson:"exact,omitempty"`
	TargetId         string `bson:"targetId,omitempty"`
	DiscordServerID  int
	SubredditID      int
	DiscordChannelID int
}

type Store interface {
	InsertDiscordServer(ctx context.Context, serverID string) (id int, err error)
	InsertDiscordChannel(ctx context.Context, channelID string, serverID int) (id int, err error)
	InsertNotification(ctx context.Context, postID, channelID, ruleID int) (id int, err error)
	InsertSubreddit(ctx context.Context, subredditID string) (id int, err error)
	InsertRule(ctx context.Context, rule Rule) (id int, err error)
	GetRules(ctx context.Context, subreddit int) ([]Rule, error)
	GetSubreddits(ctx context.Context) ([]Subreddit, error)
	GetNotificationCount(ctx context.Context, postID, channelID, ruleID int) (int, error)
}

type PGXStore struct {
	*pgxpool.Pool
}

func New(ctx ctx.Context) (*PGXStore, error) {
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

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to database: %w", err)
	}

	return &PGXStore{Pool: pool}, nil
}

func (db *PGXStore) InsertDiscordChannel(ctx context.Context, channelID string, serverID int) (id int, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sql := `INSERT INTO 
    	discord_channels (
    		channel_id, 
    	    server_id
    	) VALUES ($1, $2) ON CONFLICT (channel_id) DO NOTHING RETURNING id`
	if err := db.QueryRow(ctx, sql, channelID, serverID).Scan(&id); err != nil {
		return -1, fmt.Errorf("failed to insert data: %w", err)
	}

	return id, nil
}

func (db *PGXStore) InsertDiscordServer(ctx context.Context, serverID string) (id int, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sql := `INSERT INTO discord_servers (server_id) VALUES ($1) ON CONFLICT (server_id) DO NOTHING RETURNING id`
	if err = db.QueryRow(ctx, sql, serverID).Scan(&id); err != nil {
		return -1, fmt.Errorf("failed to insert data: %w", err)
	}

	return id, nil
}

func (db *PGXStore) InsertNotification(ctx context.Context, postID, channelID, ruleID int) (id int, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sql := `INSERT INTO notifications (post_id, channel_id, rule_id) VALUES ($1, $2, $3)`
	if err := db.QueryRow(ctx, sql, postID, channelID, ruleID).Scan(&id); err != nil {
		return -1, fmt.Errorf("failed to insert data: %w", err)
	}

	return id, nil
}

type Notification struct {
	ID        int
	PostID    int
	ChannelID int
	RuleID    int
}

func (db *PGXStore) GetNotificationCount(ctx context.Context, postID, channelID, ruleID int) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sql := `SELECT count(1) FROM notifications WHERE post_id = $1 AND channel_id = $2 AND rule_id = $3`

	row := db.QueryRow(ctx, sql, postID, channelID, ruleID)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to scan row: %w", err)
	}

	return count, nil
}

func (db *PGXStore) InsertSubreddit(ctx context.Context, subredditID string) (id int, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sql := `INSERT INTO subreddits (subreddit_id) VALUES ($1) ON CONFLICT (subreddit_id) DO NOTHING`
	if err := db.QueryRow(ctx, sql, subredditID).Scan(&id); err != nil {
		return -1, fmt.Errorf("failed to insert data: %w", err)
	}

	return id, nil
}

type Subreddit struct {
	ID          int
	SubredditID string
}

func (db *PGXStore) GetSubreddits(ctx context.Context) ([]Subreddit, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sql := `SELECT id, subreddit_id FROM subreddits`

	rows, err := db.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch subreddits: %w", err)
	}

	var subreddits []Subreddit
	for rows.Next() {
		var sr Subreddit
		if err := rows.Scan(&sr.ID, sr.SubredditID); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		subreddits = append(subreddits, sr)
	}

	return subreddits, nil
}

func (db *PGXStore) InsertRule(ctx context.Context, rule Rule) (id int, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sql := `INSERT INTO 
		rules (
		   target, 
		   target_id, 
		   exact, 
		   channel_id, 
		   subreddit_id
		) VALUES ($1, $2, $3, $4, $5)`

	if err := db.QueryRow(ctx, sql, rule.Target, rule.TargetId, rule.Exact, rule.DiscordChannelID, rule.SubredditID).Scan(&id); err != nil {
		return -1, fmt.Errorf("failed to insert data: %w", err)
	}

	return id, nil
}

func (db *PGXStore) GetRules(ctx context.Context, subreddit int) ([]Rule, error) {
	// Use a context with a timeout to avoid hanging queries
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sql := `
		SELECT 
		    r.id,
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
			&r.ID,
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
