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
	EnvPostgresURI      = "POSTGRES_ADDRESS"
	EnvPostgresDatabase = "POSTGRES_DATABASE"
	EnvPostgresUser     = "POSTGRES_USER"
	EnvPostgresPassword = "POSTGRES_PASSWORD"

	DefaultQueryTimeout = 5 * time.Second
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
	GetDiscordChannelByExternalID(ctx context.Context, channelID string) (*DiscordChannel, error)
	GetRules(ctx context.Context, subreddit int) ([]*Rule, error)
	GetRulesByChannel(ctx context.Context, channelExternalID string) ([]*RuleDetail, error)
	GetRuleByID(ctx context.Context, ruleID int) (*RuleDetail, error)
	DeleteRule(ctx context.Context, ruleID int) error
	UpdateRule(ctx context.Context, ruleID int, target string, exact bool) error
	UpdateRuleMode(ctx context.Context, ruleID int, mode string) error
	GetSubreddits(ctx context.Context) ([]*Subreddit, error)
	GetNotificationCount(ctx context.Context, postID, channelID, ruleID int) (int, error)

	GetRollingPost(ctx context.Context, channelID, subredditID int, dayLocal time.Time) (*RollingPost, error)
	UpsertRollingPost(ctx context.Context, rp RollingPost) (*RollingPost, error)

	GetLastfmListeners(ctx context.Context, artistKey string) (listeners int, fetchedAt time.Time, ok bool, err error)
	UpsertLastfmListeners(ctx context.Context, artistKey string, listeners int) error
	GetLastfmArtist(ctx context.Context, artistKey string) (listeners int, tags []string, fetchedAt time.Time, ok bool, err error)
	UpsertLastfmArtist(ctx context.Context, artistKey string, listeners int, tags []string) error

	GetPipedVideo(ctx context.Context, queryKey string) (videoID string, fetchedAt time.Time, ok bool, err error)
	UpsertPipedVideo(ctx context.Context, queryKey, videoID string) error
}

type PGXStore struct {
	*pgxpool.Pool
}

func New(ctx ctx.Ctx) (*PGXStore, error) {
	uri := os.Getenv(EnvPostgresURI)
	if uri == "" {
		return nil, fmt.Errorf("expected %s environment variable", EnvPostgresURI)
	}
	user := os.Getenv(EnvPostgresUser)
	if user == "" {
		return nil, fmt.Errorf("expected %s environment variable", EnvPostgresUser)
	}
	pass := os.Getenv(EnvPostgresPassword)
	if pass == "" {
		return nil, fmt.Errorf("expected %s environment variable", EnvPostgresPassword)
	}
	db := os.Getenv(EnvPostgresDatabase)
	if db == "" {
		return nil, fmt.Errorf("expected %s environment variable", EnvPostgresDatabase)
	}
	connString := fmt.Sprintf("postgres://%s:%s@%s/%s", user, pass, uri, db)
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, errors.New("unable to parse database config")
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("unable to connect to database")
	}

	return &PGXStore{Pool: pool}, nil
}

type DiscordServer struct {
	ID         int
	ExternalID string
}

func (db *PGXStore) InsertDiscordServer(parentCtx context.Context, serverID string) (*DiscordServer, error) {
	queryCtx, cancel := context.WithTimeout(parentCtx, DefaultQueryTimeout)
	defer cancel()

	var s DiscordServer
	query := `INSERT INTO discord_servers (server_id) VALUES (lower($1)) ON CONFLICT (server_id) DO NOTHING RETURNING id, server_id`
	if err := db.QueryRow(queryCtx, query, serverID).Scan(&s.ID, &s.ExternalID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.GetDiscordServerByExternalID(parentCtx, serverID)
		}
		return nil, fmt.Errorf("failed to insert server: %w", err)
	}

	return &s, nil
}

func (db *PGXStore) GetDiscordServerByExternalID(ctx context.Context, serverID string) (*DiscordServer, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	query := `SELECT id, server_id FROM discord_servers where server_id = lower($1)`

	row := db.QueryRow(ctx, query, serverID)
	var ch DiscordServer
	if err := row.Scan(&ch.ID, &ch.ExternalID); err != nil {
		return nil, fmt.Errorf("failed to get discord server %q: %w", serverID, err)
	}

	return &ch, nil
}

type DiscordChannel struct {
	ID         int
	ExternalID string
}

func (db *PGXStore) InsertDiscordChannel(parentCtx context.Context, channelID string, serverID int) (*DiscordChannel, error) {
	queryCtx, cancel := context.WithTimeout(parentCtx, DefaultQueryTimeout)
	defer cancel()

	var c DiscordChannel
	query := `INSERT INTO
    	discord_channels (
    		channel_id,
    	    server_id
    	) VALUES (lower($1), $2) ON CONFLICT (channel_id) DO NOTHING RETURNING id, channel_id`
	if err := db.QueryRow(queryCtx, query, channelID, serverID).Scan(&c.ID, &c.ExternalID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.GetDiscordChannelByExternalID(parentCtx, channelID)
		}
		return nil, fmt.Errorf("failed to insert channel: %w", err)
	}

	return &c, nil
}

func (db *PGXStore) GetDiscordChannel(ctx context.Context, channelID int) (*DiscordChannel, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	query := `SELECT id, channel_id FROM discord_channels where id = $1`

	row := db.QueryRow(ctx, query, channelID)
	var ch DiscordChannel
	if err := row.Scan(&ch.ID, &ch.ExternalID); err != nil {
		return nil, fmt.Errorf("failed to get discord channel %d: %w", channelID, err)
	}

	return &ch, nil
}

func (db *PGXStore) GetDiscordChannelByExternalID(ctx context.Context, channelID string) (*DiscordChannel, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	query := `SELECT id, channel_id FROM discord_channels where channel_id = lower($1)`

	row := db.QueryRow(ctx, query, channelID)
	var ch DiscordChannel
	if err := row.Scan(&ch.ID, &ch.ExternalID); err != nil {
		return nil, fmt.Errorf("failed to get discord channel %q: %w", channelID, err)
	}

	return &ch, nil
}

type Notification struct {
	ID        int
	PostID    int
	ChannelID int
	RuleID    int
}

func (db *PGXStore) InsertNotification(parentCtx context.Context, postID, channelID, ruleID int) (*Notification, error) {
	queryCtx, cancel := context.WithTimeout(parentCtx, DefaultQueryTimeout)
	defer cancel()

	var n Notification
	query := `INSERT INTO notifications (post_id, channel_id, rule_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING RETURNING id, post_id, channel_id, rule_id`
	if err := db.QueryRow(queryCtx, query, postID, channelID, ruleID).Scan(&n.ID, &n.PostID, &n.ChannelID, &n.RuleID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.GetNotification(parentCtx, postID, channelID, ruleID)
		}
		return nil, fmt.Errorf("failed to insert notification: %w", err)
	}

	return &n, nil
}

func (db *PGXStore) GetNotification(ctx context.Context, postID, channelID, ruleID int) (*Notification, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	var n Notification
	query := `SELECT id, post_id, channel_id, rule_id FROM notifications WHERE post_id = $1 AND channel_id = $2 AND rule_id = $3`
	row := db.QueryRow(ctx, query, postID, channelID, ruleID)
	if err := row.Scan(&n.ID, &n.PostID, &n.ChannelID, &n.RuleID); err != nil {
		return nil, fmt.Errorf("failed to get notification: %w", err)
	}
	return &n, nil
}

func (db *PGXStore) GetNotificationCount(ctx context.Context, postID, channelID, ruleID int) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	query := `SELECT count(1) FROM notifications WHERE post_id = $1 AND channel_id = $2 AND rule_id = $3`

	row := db.QueryRow(ctx, query, postID, channelID, ruleID)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to get notification count: %w", err)
	}

	return count, nil
}

type Post struct {
	ID         int
	ExternalID string
}

func (db *PGXStore) InsertPost(parentCtx context.Context, postID string) (*Post, error) {
	queryCtx, cancel := context.WithTimeout(parentCtx, DefaultQueryTimeout)
	defer cancel()

	var p Post
	query := `INSERT INTO posts (post_id) VALUES (lower($1)) ON CONFLICT (post_id) DO NOTHING RETURNING id, post_id`
	if err := db.QueryRow(queryCtx, query, postID).Scan(&p.ID, &p.ExternalID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.GetPostByExternalID(parentCtx, postID)
		}
		return nil, fmt.Errorf("failed to insert post: %w", err)
	}

	return &p, nil
}

func (db *PGXStore) GetPostByExternalID(ctx context.Context, postID string) (*Post, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	query := `SELECT id, post_id FROM posts WHERE post_id = lower($1)`
	var p Post
	if err := db.QueryRow(ctx, query, postID).Scan(&p.ID, &p.ExternalID); err != nil {
		return nil, fmt.Errorf("failed to get post %q: %w", postID, err)
	}

	return &p, nil
}

type Subreddit struct {
	ID         int
	ExternalID string
}

func (db *PGXStore) InsertSubreddit(parentCtx context.Context, subredditID string) (*Subreddit, error) {
	queryCtx, cancel := context.WithTimeout(parentCtx, DefaultQueryTimeout)
	defer cancel()

	var s Subreddit
	query := `INSERT INTO subreddits (subreddit_id) VALUES (lower($1)) ON CONFLICT (subreddit_id) DO NOTHING RETURNING id, subreddit_id`
	if err := db.QueryRow(queryCtx, query, subredditID).Scan(&s.ID, &s.ExternalID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.GetSubredditByExternalID(parentCtx, subredditID)
		}
		return nil, fmt.Errorf("failed to insert subreddit: %w", err)
	}

	return &s, nil
}

func (db *PGXStore) GetSubredditByExternalID(ctx context.Context, subredditID string) (*Subreddit, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	query := `SELECT id, subreddit_id FROM subreddits where subreddit_id = lower($1)`

	row := db.QueryRow(ctx, query, subredditID)
	var sr Subreddit
	if err := row.Scan(&sr.ID, &sr.ExternalID); err != nil {
		return nil, fmt.Errorf("failed to get subreddit %q: %w", subredditID, err)
	}

	return &sr, nil
}

func (db *PGXStore) GetSubreddits(ctx context.Context) ([]*Subreddit, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	query := `SELECT id, subreddit_id FROM subreddits`

	rows, err := db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch subreddits: %w", err)
	}
	defer rows.Close()

	var subreddits []*Subreddit
	for rows.Next() {
		var sr Subreddit
		if err := rows.Scan(&sr.ID, &sr.ExternalID); err != nil {
			return nil, fmt.Errorf("failed to scan subreddit row: %w", err)
		}
		subreddits = append(subreddits, &sr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating subreddit rows: %w", err)
	}

	return subreddits, nil
}

type Rule struct {
	ID               int
	Target           string
	Exact            bool
	TargetID         string
	Mode             string // "narrative" | "music" | "summary" | "media"
	DiscordServerID  int
	SubredditID      int
	DiscordChannelID int
}

func (db *PGXStore) InsertRule(ctx context.Context, rule Rule) (*Rule, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	if rule.Mode == "" {
		rule.Mode = "narrative"
	}

	query := `INSERT INTO
		rules (
		   target,
		   target_id,
		   exact,
		   channel_id,
		   subreddit_id,
		   mode
		) VALUES (lower($1), lower($2), $3, $4, $5, $6) RETURNING id`

	if err := db.QueryRow(ctx, query, rule.Target, rule.TargetID, rule.Exact, rule.DiscordChannelID, rule.SubredditID, rule.Mode).Scan(&rule.ID); err != nil {
		return nil, fmt.Errorf("failed to insert rule: %w", err)
	}

	return &rule, nil
}

func (db *PGXStore) GetRules(ctx context.Context, subreddit int) ([]*Rule, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	query := `
		SELECT
		    r.id,
		    target,
		    target_id,
		    exact,
		    COALESCE(r.mode, 'narrative'),
		    ds.id,
		    dc.id,
		    sr.id
		FROM rules r
			JOIN subreddits sr ON r.subreddit_id = sr.id
			JOIN discord_channels dc on r.channel_id = dc.id
			JOIN discord_servers ds on dc.server_id = ds.id
		WHERE sr.id = $1
	`

	rows, err := db.Query(ctx, query, subreddit)
	if err != nil {
		return nil, fmt.Errorf("failed to query rules: %w", err)
	}
	defer rows.Close()

	var rules []*Rule
	for rows.Next() {
		var r Rule
		if err := rows.Scan(
			&r.ID,
			&r.Target,
			&r.TargetID,
			&r.Exact,
			&r.Mode,
			&r.DiscordServerID,
			&r.DiscordChannelID,
			&r.SubredditID,
		); err != nil {
			return nil, fmt.Errorf("failed to scan rule row: %w", err)
		}
		rules = append(rules, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating rule rows: %w", err)
	}

	return rules, nil
}

type RuleDetail struct {
	ID        int
	Target    string
	Exact     bool
	TargetID  string
	Mode      string
	Subreddit string
	ServerID  int
}

func (db *PGXStore) GetRulesByChannel(ctx context.Context, channelExternalID string) ([]*RuleDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	query := `
		SELECT r.id, r.target, r.exact, r.target_id, COALESCE(r.mode, 'narrative'), sr.subreddit_id, ds.id
		FROM rules r
			JOIN subreddits sr ON r.subreddit_id = sr.id
			JOIN discord_channels dc ON r.channel_id = dc.id
			JOIN discord_servers ds ON dc.server_id = ds.id
		WHERE dc.channel_id = lower($1)
		ORDER BY r.id
	`

	rows, err := db.Query(ctx, query, channelExternalID)
	if err != nil {
		return nil, fmt.Errorf("failed to query rules by channel: %w", err)
	}
	defer rows.Close()

	var rules []*RuleDetail
	for rows.Next() {
		var r RuleDetail
		if err := rows.Scan(&r.ID, &r.Target, &r.Exact, &r.TargetID, &r.Mode, &r.Subreddit, &r.ServerID); err != nil {
			return nil, fmt.Errorf("failed to scan rule detail row: %w", err)
		}
		rules = append(rules, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating rule detail rows: %w", err)
	}

	return rules, nil
}

func (db *PGXStore) GetRuleByID(ctx context.Context, ruleID int) (*RuleDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	query := `
		SELECT r.id, r.target, r.exact, r.target_id, COALESCE(r.mode, 'narrative'), sr.subreddit_id, ds.id
		FROM rules r
			JOIN subreddits sr ON r.subreddit_id = sr.id
			JOIN discord_channels dc ON r.channel_id = dc.id
			JOIN discord_servers ds ON dc.server_id = ds.id
		WHERE r.id = $1
	`

	var r RuleDetail
	if err := db.QueryRow(ctx, query, ruleID).Scan(&r.ID, &r.Target, &r.Exact, &r.TargetID, &r.Mode, &r.Subreddit, &r.ServerID); err != nil {
		return nil, fmt.Errorf("failed to get rule %d: %w", ruleID, err)
	}

	return &r, nil
}

// UpdateRuleMode changes a rule's digest mode. Accepted values mirror the
// discord/digest packages' Mode constants: "narrative", "music", "summary",
// "media". Returns ErrNoRows-equivalent by checking rows-affected.
func (db *PGXStore) UpdateRuleMode(ctx context.Context, ruleID int, mode string) error {
	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	tag, err := db.Exec(ctx, `UPDATE rules SET mode = $1 WHERE id = $2`, mode, ruleID)
	if err != nil {
		return fmt.Errorf("failed to update rule mode: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("rule %d not found", ruleID)
	}
	return nil
}

func (db *PGXStore) DeleteRule(ctx context.Context, ruleID int) error {
	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	query := `DELETE FROM rules WHERE id = $1`
	tag, err := db.Exec(ctx, query, ruleID)
	if err != nil {
		return fmt.Errorf("failed to delete rule %d: %w", ruleID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("rule %d not found", ruleID)
	}

	return nil
}

func (db *PGXStore) UpdateRule(ctx context.Context, ruleID int, target string, exact bool) error {
	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	query := `UPDATE rules SET target = lower($1), exact = $2 WHERE id = $3`
	tag, err := db.Exec(ctx, query, target, exact, ruleID)
	if err != nil {
		return fmt.Errorf("failed to update rule %d: %w", ruleID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("rule %d not found", ruleID)
	}

	return nil
}

// RollingPost is one Discord digest message that accumulates all rule matches
// from a single subreddit on a single America/Phoenix local day. First match
// of the day inserts the row and sends a new Discord message; subsequent
// matches update the same row and edit the same Discord message in place.
type RollingPost struct {
	ID                int
	ChannelID         int
	SubredditID       int
	DayLocal          time.Time
	Mode              string // narrative | music | summary | media
	DiscordMessageIDs []string
	NarrativeTitle    string
	NarrativeSummary  string
	Entries           []byte // mode-specific JSON payload; narrative mode leaves this empty
	IncludedPostIDs   []string
	IncludedRuleIDs   []int
	LatestScore       int
	LatestComments    int
	LatestURL         string
	LatestThumbnail   string
	UpdatedAt         time.Time
}

// GetRollingPost returns the rolling digest for (channel, subreddit, day_local)
// if one exists. Returns (nil, nil) when there is no row yet — the caller uses
// that to drive the Fresh vs Update branching.
func (db *PGXStore) GetRollingPost(parent context.Context, channelID, subredditID int, dayLocal time.Time) (*RollingPost, error) {
	qctx, cancel := context.WithTimeout(parent, DefaultQueryTimeout)
	defer cancel()

	query := `
		SELECT id, channel_id, subreddit_id, day_local,
		       COALESCE(mode, 'narrative'),
		       COALESCE(discord_message_ids, '{}'::text[]),
		       narrative_title, narrative_summary,
		       COALESCE(entries, '[]'::jsonb),
		       included_post_ids, included_rule_ids,
		       latest_score, latest_comments, latest_url,
		       latest_thumbnail, updated_at
		FROM rolling_posts
		WHERE channel_id = $1 AND subreddit_id = $2 AND day_local = $3
	`
	var rp RollingPost
	err := db.QueryRow(qctx, query, channelID, subredditID, dayLocal).Scan(
		&rp.ID, &rp.ChannelID, &rp.SubredditID, &rp.DayLocal,
		&rp.Mode, &rp.DiscordMessageIDs,
		&rp.NarrativeTitle, &rp.NarrativeSummary, &rp.Entries,
		&rp.IncludedPostIDs, &rp.IncludedRuleIDs,
		&rp.LatestScore, &rp.LatestComments, &rp.LatestURL,
		&rp.LatestThumbnail, &rp.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get rolling post: %w", err)
	}
	return &rp, nil
}

// GetLastfmListeners returns the cached listener count for an artist key.
// The ok return is false on cache miss (no error). Callers decide whether
// a stale fetched_at warrants a refetch.
func (db *PGXStore) GetLastfmListeners(parent context.Context, artistKey string) (int, time.Time, bool, error) {
	qctx, cancel := context.WithTimeout(parent, DefaultQueryTimeout)
	defer cancel()

	var listeners int
	var fetchedAt time.Time
	err := db.QueryRow(qctx, `SELECT listeners, fetched_at FROM lastfm_cache WHERE artist_key = $1`, artistKey).Scan(&listeners, &fetchedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, time.Time{}, false, nil
		}
		return 0, time.Time{}, false, fmt.Errorf("failed to read lastfm cache: %w", err)
	}
	return listeners, fetchedAt, true, nil
}

// UpsertLastfmListeners writes a listener count for an artist key, refreshing
// fetched_at so the TTL restarts.
func (db *PGXStore) UpsertLastfmListeners(parent context.Context, artistKey string, listeners int) error {
	qctx, cancel := context.WithTimeout(parent, DefaultQueryTimeout)
	defer cancel()

	_, err := db.Exec(qctx, `
		INSERT INTO lastfm_cache (artist_key, listeners, fetched_at)
		VALUES ($1, $2, now())
		ON CONFLICT (artist_key) DO UPDATE
		SET listeners = EXCLUDED.listeners, fetched_at = now()
	`, artistKey, listeners)
	if err != nil {
		return fmt.Errorf("failed to upsert lastfm cache: %w", err)
	}
	return nil
}

// GetLastfmArtist returns cached listeners + tags together.
func (db *PGXStore) GetLastfmArtist(parent context.Context, artistKey string) (int, []string, time.Time, bool, error) {
	qctx, cancel := context.WithTimeout(parent, DefaultQueryTimeout)
	defer cancel()

	var listeners int
	var tags []string
	var fetchedAt time.Time
	err := db.QueryRow(qctx, `SELECT listeners, COALESCE(tags, '{}'::text[]), fetched_at FROM lastfm_cache WHERE artist_key = $1`, artistKey).
		Scan(&listeners, &tags, &fetchedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, time.Time{}, false, nil
		}
		return 0, nil, time.Time{}, false, fmt.Errorf("failed to read lastfm cache: %w", err)
	}
	return listeners, tags, fetchedAt, true, nil
}

// GetPipedVideo returns the cached Piped search hit for a normalized query
// key. The returned string is the full music.youtube.com URL (watch OR
// playlist) — empty string is a legitimate cached "no match" outcome.
// The ok return is false on cache miss (no error).
func (db *PGXStore) GetPipedVideo(parent context.Context, queryKey string) (string, time.Time, bool, error) {
	qctx, cancel := context.WithTimeout(parent, DefaultQueryTimeout)
	defer cancel()

	var youtubeURL string
	var fetchedAt time.Time
	err := db.QueryRow(qctx, `SELECT youtube_url, fetched_at FROM piped_cache WHERE query_key = $1`, queryKey).
		Scan(&youtubeURL, &fetchedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", time.Time{}, false, nil
		}
		return "", time.Time{}, false, fmt.Errorf("failed to read piped cache: %w", err)
	}
	return youtubeURL, fetchedAt, true, nil
}

// UpsertPipedVideo writes a Piped search hit, refreshing fetched_at. Empty
// youtubeURL is a legitimate cacheable outcome (search returned nothing).
func (db *PGXStore) UpsertPipedVideo(parent context.Context, queryKey, youtubeURL string) error {
	qctx, cancel := context.WithTimeout(parent, DefaultQueryTimeout)
	defer cancel()
	_, err := db.Exec(qctx, `
		INSERT INTO piped_cache (query_key, youtube_url, fetched_at)
		VALUES ($1, $2, now())
		ON CONFLICT (query_key) DO UPDATE
		SET youtube_url = EXCLUDED.youtube_url, fetched_at = now()
	`, queryKey, youtubeURL)
	if err != nil {
		return fmt.Errorf("failed to upsert piped cache: %w", err)
	}
	return nil
}

// UpsertLastfmArtist writes listeners + tags together, refreshing fetched_at.
func (db *PGXStore) UpsertLastfmArtist(parent context.Context, artistKey string, listeners int, tags []string) error {
	qctx, cancel := context.WithTimeout(parent, DefaultQueryTimeout)
	defer cancel()
	if tags == nil {
		tags = []string{}
	}
	_, err := db.Exec(qctx, `
		INSERT INTO lastfm_cache (artist_key, listeners, tags, fetched_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (artist_key) DO UPDATE
		SET listeners = EXCLUDED.listeners,
		    tags      = EXCLUDED.tags,
		    fetched_at = now()
	`, artistKey, listeners, tags)
	if err != nil {
		return fmt.Errorf("failed to upsert lastfm artist cache: %w", err)
	}
	return nil
}

// UpsertRollingPost inserts or updates the rolling digest row keyed by
// (channel_id, subreddit_id, day_local). On conflict, all mutable fields are
// overwritten with the supplied values and updated_at is bumped.
func (db *PGXStore) UpsertRollingPost(parent context.Context, rp RollingPost) (*RollingPost, error) {
	qctx, cancel := context.WithTimeout(parent, DefaultQueryTimeout)
	defer cancel()

	entries := rp.Entries
	if len(entries) == 0 {
		entries = []byte("[]")
	}
	if rp.Mode == "" {
		rp.Mode = "narrative"
	}
	query := `
		INSERT INTO rolling_posts (
			channel_id, subreddit_id, day_local,
			mode, discord_message_ids,
			narrative_title, narrative_summary, entries,
			included_post_ids, included_rule_ids,
			latest_score, latest_comments, latest_url,
			latest_thumbnail, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, now())
		ON CONFLICT (channel_id, subreddit_id, day_local) DO UPDATE SET
			mode                = EXCLUDED.mode,
			discord_message_ids = EXCLUDED.discord_message_ids,
			narrative_title     = EXCLUDED.narrative_title,
			narrative_summary   = EXCLUDED.narrative_summary,
			entries             = EXCLUDED.entries,
			included_post_ids   = EXCLUDED.included_post_ids,
			included_rule_ids   = EXCLUDED.included_rule_ids,
			latest_score        = EXCLUDED.latest_score,
			latest_comments     = EXCLUDED.latest_comments,
			latest_url          = EXCLUDED.latest_url,
			latest_thumbnail    = EXCLUDED.latest_thumbnail,
			updated_at          = now()
		RETURNING id, channel_id, subreddit_id, day_local,
		          COALESCE(mode, 'narrative'),
		          COALESCE(discord_message_ids, '{}'::text[]),
		          narrative_title, narrative_summary,
		          COALESCE(entries, '[]'::jsonb),
		          included_post_ids, included_rule_ids,
		          latest_score, latest_comments, latest_url,
		          latest_thumbnail, updated_at
	`
	var out RollingPost
	err := db.QueryRow(qctx, query,
		rp.ChannelID, rp.SubredditID, rp.DayLocal,
		rp.Mode, rp.DiscordMessageIDs,
		rp.NarrativeTitle, rp.NarrativeSummary, entries,
		rp.IncludedPostIDs, rp.IncludedRuleIDs,
		rp.LatestScore, rp.LatestComments, rp.LatestURL,
		rp.LatestThumbnail,
	).Scan(
		&out.ID, &out.ChannelID, &out.SubredditID, &out.DayLocal,
		&out.Mode, &out.DiscordMessageIDs,
		&out.NarrativeTitle, &out.NarrativeSummary, &out.Entries,
		&out.IncludedPostIDs, &out.IncludedRuleIDs,
		&out.LatestScore, &out.LatestComments, &out.LatestURL,
		&out.LatestThumbnail, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert rolling post: %w", err)
	}
	return &out, nil
}
