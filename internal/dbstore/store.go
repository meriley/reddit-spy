package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jackc/pgx/v5/pgxpool"

	ctx "github.com/meriley/reddit-spy/internal/context"
)

const (
	EnvPostgresURI      = "postgres.address"
	EnvPostgresDatabase = "postgres.database"
	EnvPostgresUser     = "postgres.user"
	EnvPostgresPassword = "postgres.password"
)

type Store interface {
	InsertDiscordServer(ctx context.Context, serverID string) (*DiscordServer, error)
	InsertDiscordChannel(ctx context.Context, channelID string, serverID int) (*DiscordChannel, error)
	InsertNotification(ctx context.Context, postID, channelID, ruleID int) (*Notification, error)
	InsertSubreddit(ctx context.Context, subredditID string) (*Subreddit, error)
	InsertRule(ctx context.Context, rule Rule) (*Rule, error)
	InsertPost(ctx context.Context, postID string) (*Post, error)

	GetDiscordServerByExternalID(ctx context.Context, serverID string) (*DiscordServer, error)
	GetSubredditByExternalID(ctx context.Context, subreddit string) (*Subreddit, error)

	GetDiscordChannel(ctx context.Context, channelID int) (*DiscordChannel, error)
	GetRules(ctx context.Context, subreddit int) ([]*Rule, error)
	GetSubreddits(ctx context.Context) ([]*Subreddit, error)
	GetNotificationCount(ctx context.Context, postID, channelID, ruleID int) (int, error)
}

type PGXStore struct {
	*pgxpool.Pool
}

func New(ctx ctx.Ctx) (*PGXStore, error) {
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

type DiscordServer struct {
	ID         int
	ExternalID string
}

func (db *PGXStore) InsertDiscordServer(ctx context.Context, serverID string) (*DiscordServer, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var s DiscordServer
	sql := `INSERT INTO discord_servers (server_id) VALUES (lower($1)) ON CONFLICT (server_id) DO NOTHING RETURNING id, server_id`
	if err := db.QueryRow(ctx, sql, serverID).Scan(&s.ID, &s.ExternalID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.GetDiscordServerByExternalID(ctx, serverID)
		}
		return nil, fmt.Errorf("failed to insert server: %w", err)
	}

	return &s, nil
}

func (db *PGXStore) GetDiscordServerByExternalID(ctx context.Context, serverID string) (*DiscordServer, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sql := `SELECT id, server_id FROM discord_servers where server_id = lower($1)`

	row := db.QueryRow(ctx, sql, serverID)
	var ch DiscordServer
	if err := row.Scan(&ch.ID, &ch.ExternalID); err != nil {
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}

	return &ch, nil
}

type DiscordChannel struct {
	ID         int
	ExternalID string
}

func (db *PGXStore) InsertDiscordChannel(ctx context.Context, channelID string, serverID int) (*DiscordChannel, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var c DiscordChannel
	sql := `INSERT INTO 
    	discord_channels (
    		channel_id, 
    	    server_id
    	) VALUES (lower($1), $2) ON CONFLICT (channel_id) DO NOTHING RETURNING id, channel_id`
	if err := db.QueryRow(ctx, sql, channelID, serverID).Scan(&c.ID, &c.ExternalID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.GetDiscordChannelByExternalID(ctx, channelID)
		}
		return nil, fmt.Errorf("failed to insert channel: %w", err)
	}

	return &c, nil
}

func (db *PGXStore) GetDiscordChannel(ctx context.Context, channelID int) (*DiscordChannel, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sql := `SELECT id, channel_id FROM discord_channels where id = $1`

	row := db.QueryRow(ctx, sql, channelID)
	var ch DiscordChannel
	if err := row.Scan(&ch.ID, &ch.ExternalID); err != nil {
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}

	return &ch, nil
}

func (db *PGXStore) GetDiscordChannelByExternalID(ctx context.Context, channelID string) (*DiscordChannel, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sql := `SELECT id, channel_id FROM discord_channels where channel_id = lower($1)`

	row := db.QueryRow(ctx, sql, channelID)
	var ch DiscordChannel
	if err := row.Scan(&ch.ID, &ch.ExternalID); err != nil {
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}

	return &ch, nil
}

type Notification struct {
	ID        int
	PostID    int
	ChannelID int
	RuleID    int
}

func (db *PGXStore) InsertNotification(ctx context.Context, postID, channelID, ruleID int) (*Notification, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var n Notification
	sql := `INSERT INTO notifications (post_id, channel_id, rule_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING RETURNING id, post_id, channel_id, rule_id`
	if err := db.QueryRow(ctx, sql, postID, channelID, ruleID).Scan(&n.ID, &n.PostID, &n.ChannelID, &n.RuleID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.GetNotification(ctx, postID, channelID, ruleID)
		}
		return nil, fmt.Errorf("failed to insert data: %w", err)
	}

	return &n, nil
}

func (db *PGXStore) GetNotification(ctx context.Context, postID, channelID, ruleID int) (*Notification, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var n Notification
	sql := `SELECT id, post_id, channel_id, rule_id FROM notifications WHERE post_id = $1 AND channel_id = $2 AND rule_id = $3`
	row := db.QueryRow(ctx, sql, postID, channelID, ruleID)
	if err := row.Scan(&n.ID, &n.PostID, &n.ChannelID, &n.RuleID); err != nil {
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}
	return &n, nil
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

type Post struct {
	ID         int
	ExternalID string
}

func (db *PGXStore) InsertPost(ctx context.Context, postID string) (*Post, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var p Post
	sql := `INSERT INTO posts (post_id) VALUES (lower($1)) ON CONFLICT (post_id) DO NOTHING RETURNING id, post_id`
	if err := db.QueryRow(ctx, sql, postID).Scan(&p.ID, &p.ExternalID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.GetPostByExternalID(ctx, postID)
		}
		return nil, fmt.Errorf("failed to insert data: %w", err)
	}

	return &p, nil
}

func (db *PGXStore) GetPostByExternalID(ctx context.Context, postID string) (*Post, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sql := `SELECT id, post_id FROM posts WHERE post_id = lower($1)`
	var p Post
	if err := db.QueryRow(ctx, sql, postID).Scan(&p.ID, &p.ExternalID); err != nil {
		return nil, fmt.Errorf("failed to insert data: %w", err)
	}

	return &p, nil
}

type Subreddit struct {
	ID         int
	ExternalID string
}

func (db *PGXStore) InsertSubreddit(ctx context.Context, subredditID string) (*Subreddit, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var s Subreddit
	sql := `INSERT INTO subreddits (subreddit_id) VALUES (lower($1)) ON CONFLICT (subreddit_id) DO NOTHING RETURNING id, subreddit_id`
	if err := db.QueryRow(ctx, sql, subredditID).Scan(&s.ID, &s.ExternalID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.GetSubredditByExternalID(ctx, subredditID)
		}
		return nil, fmt.Errorf("failed to insert data: %w", err)
	}

	return &s, nil
}

func (db *PGXStore) GetSubredditByExternalID(ctx context.Context, subredditID string) (*Subreddit, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sql := `SELECT id, subreddit_id FROM subreddits where subreddit_id = lower($1)`

	row := db.QueryRow(ctx, sql, subredditID)
	var sr Subreddit
	if err := row.Scan(&sr.ID, &sr.ExternalID); err != nil {
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}

	return &sr, nil
}

func (db *PGXStore) GetSubreddits(ctx context.Context) ([]*Subreddit, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sql := `SELECT id, subreddit_id FROM subreddits`

	rows, err := db.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch subreddits: %w", err)
	}

	var subreddits []*Subreddit
	for rows.Next() {
		var sr Subreddit
		if err := rows.Scan(&sr.ID, &sr.ExternalID); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		subreddits = append(subreddits, &sr)
	}

	return subreddits, nil
}

type Rule struct {
	ID               int
	Target           string
	Exact            bool
	TargetId         string
	DiscordServerID  int
	SubredditID      int
	DiscordChannelID int
}

func (db *PGXStore) InsertRule(ctx context.Context, rule Rule) (*Rule, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sql := `INSERT INTO 
		rules (
		   target, 
		   target_id, 
		   exact, 
		   channel_id, 
		   subreddit_id
		) VALUES (lower($1), lower($2), $3, $4, $5) RETURNING id`

	if err := db.QueryRow(ctx, sql, rule.Target, rule.TargetId, rule.Exact, rule.DiscordChannelID, rule.SubredditID).Scan(&rule.ID); err != nil {
		return nil, fmt.Errorf("failed to insert data: %w", err)
	}

	return &rule, nil
}

func (db *PGXStore) GetRules(ctx context.Context, subreddit int) ([]*Rule, error) {
	// Use a context with a timeout to avoid hanging queries
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sql := `
		SELECT 
		    r.id,
		    target, 
		    target_id, 
		    exact,
		    ds.id,
		    dc.id, 
		    sr.id 
		FROM rules r
			JOIN subreddits sr ON r.subreddit_id = sr.id
			JOIN discord_channels dc on r.channel_id = dc.id
			JOIN discord_servers ds on dc.server_id = ds.id
		WHERE sr.id = $1
	`

	rows, err := db.Query(ctx, sql, subreddit)
	if err != nil {
		return nil, fmt.Errorf("failed to query data: %w", err)
	}
	defer rows.Close()

	var rules []*Rule
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
		rules = append(rules, &r)
	}

	return rules, nil
}
