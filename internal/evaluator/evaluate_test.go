package evaluator

import (
	"context"
	"testing"
	"time"

	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	redditJson "github.com/meriley/reddit-spy/internal/redditJSON"
)

func TestEvaluateExact(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
		want     bool
	}{
		{"exact match", "hello", "hello", true},
		{"case insensitive", "Hello", "hello", true},
		{"no match", "hello", "world", false},
		{"empty strings", "", "", true},
		{"partial not exact", "hello world", "hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evaluateExact(tt.value, tt.expected)
			if got != tt.want {
				t.Errorf("evaluateExact(%q, %q) = %v, want %v", tt.value, tt.expected, got, tt.want)
			}
		})
	}
}

func TestEvaluatePartial(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
		want     bool
	}{
		{"contains match", "hello world", "hello", true},
		{"case insensitive", "Hello World", "hello", true},
		{"exact is partial", "hello", "hello", true},
		{"no match", "hello", "world", false},
		{"empty pattern", "hello", "", true},
		{"empty value", "", "hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evaluatePartial(tt.value, tt.expected)
			if got != tt.want {
				t.Errorf("evaluatePartial(%q, %q) = %v, want %v", tt.value, tt.expected, got, tt.want)
			}
		})
	}
}

func TestGetValue(t *testing.T) {
	post := &redditJson.RedditPost{
		Author: "testuser",
		Title:  "test title",
	}

	tests := []struct {
		name     string
		targetID string
		want     string
		wantErr  bool
	}{
		{"author", "author", "testuser", false},
		{"title", "title", "test title", false},
		{"unknown", "body", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &dbstore.Rule{TargetID: tt.targetID}
			got, err := getValue(post, rule)
			if (err != nil) != tt.wantErr {
				t.Errorf("getValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getValue() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewRuleEvaluator(t *testing.T) {
	store := &mockStore{}
	eval := NewRuleEvaluator(store)

	if eval == nil {
		t.Fatal("NewRuleEvaluator returned nil")
		return
	}
	if eval.EvaluateResponseChannel == nil {
		t.Fatal("EvaluateResponseChannel is nil")
		return
	}
	if cap(eval.EvaluateResponseChannel) != EvalChannelBuffer {
		t.Errorf("channel capacity = %d, want %d", cap(eval.EvaluateResponseChannel), EvalChannelBuffer)
	}
}

// mockStore implements dbstore.Store for testing
type mockStore struct{}

func (m *mockStore) InsertDiscordServer(_ context.Context, _ string) (*dbstore.DiscordServer, error) {
	return &dbstore.DiscordServer{ID: 1}, nil
}
func (m *mockStore) InsertDiscordChannel(_ context.Context, _ string, _ int) (*dbstore.DiscordChannel, error) {
	return &dbstore.DiscordChannel{ID: 1}, nil
}
func (m *mockStore) InsertNotification(_ context.Context, _, _, _ int) (*dbstore.Notification, error) {
	return &dbstore.Notification{ID: 1}, nil
}
func (m *mockStore) InsertSubreddit(_ context.Context, _ string) (*dbstore.Subreddit, error) {
	return &dbstore.Subreddit{ID: 1}, nil
}
func (m *mockStore) InsertRule(_ context.Context, rule dbstore.Rule) (*dbstore.Rule, error) {
	rule.ID = 1
	return &rule, nil
}
func (m *mockStore) InsertPost(_ context.Context, _ string) (*dbstore.Post, error) {
	return &dbstore.Post{ID: 1}, nil
}
func (m *mockStore) GetDiscordServerByExternalID(_ context.Context, _ string) (*dbstore.DiscordServer, error) {
	return &dbstore.DiscordServer{ID: 1}, nil
}
func (m *mockStore) GetSubredditByExternalID(_ context.Context, _ string) (*dbstore.Subreddit, error) {
	return &dbstore.Subreddit{ID: 1, ExternalID: "golang"}, nil
}
func (m *mockStore) GetDiscordChannel(_ context.Context, _ int) (*dbstore.DiscordChannel, error) {
	return &dbstore.DiscordChannel{ID: 1}, nil
}
func (m *mockStore) GetDiscordChannelByExternalID(_ context.Context, _ string) (*dbstore.DiscordChannel, error) {
	return &dbstore.DiscordChannel{ID: 1}, nil
}
func (m *mockStore) GetRules(_ context.Context, _ int) ([]*dbstore.Rule, error) {
	return []*dbstore.Rule{
		{ID: 1, Target: "test", TargetID: "title", Exact: false, DiscordChannelID: 1},
	}, nil
}
func (m *mockStore) GetRulesByChannel(_ context.Context, _ string) ([]*dbstore.RuleDetail, error) {
	return nil, nil
}
func (m *mockStore) GetRuleByID(_ context.Context, _ int) (*dbstore.RuleDetail, error) {
	return &dbstore.RuleDetail{ID: 1}, nil
}
func (m *mockStore) DeleteRule(_ context.Context, _ int) error { return nil }
func (m *mockStore) UpdateRule(_ context.Context, _ int, _ string, _ bool) error {
	return nil
}
func (m *mockStore) UpdateRuleMode(_ context.Context, _ int, _ string) error {
	return nil
}
func (m *mockStore) GetLastfmListeners(_ context.Context, _ string) (int, time.Time, bool, error) {
	return 0, time.Time{}, false, nil
}
func (m *mockStore) UpsertLastfmListeners(_ context.Context, _ string, _ int) error {
	return nil
}
func (m *mockStore) GetLastfmArtist(_ context.Context, _ string) (int, []string, time.Time, bool, error) {
	return 0, nil, time.Time{}, false, nil
}
func (m *mockStore) UpsertLastfmArtist(_ context.Context, _ string, _ int, _ []string) error {
	return nil
}
func (m *mockStore) GetPipedVideo(_ context.Context, _ string) (string, time.Time, bool, error) {
	return "", time.Time{}, false, nil
}
func (m *mockStore) UpsertPipedVideo(_ context.Context, _, _ string) error { return nil }
func (m *mockStore) GetQobuzAlbum(_ context.Context, _ string) (string, time.Time, bool, error) {
	return "", time.Time{}, false, nil
}
func (m *mockStore) UpsertQobuzAlbum(_ context.Context, _, _ string) error { return nil }
func (m *mockStore) GetSubreddits(_ context.Context) ([]*dbstore.Subreddit, error) {
	return nil, nil
}
func (m *mockStore) GetNotificationCount(_ context.Context, _, _, _ int) (int, error) {
	return 0, nil
}
func (m *mockStore) GetRollingPost(_ context.Context, _, _ int, _ time.Time) (*dbstore.RollingPost, error) {
	return nil, nil
}
func (m *mockStore) UpsertRollingPost(_ context.Context, _ dbstore.RollingPost) (*dbstore.RollingPost, error) {
	return nil, nil
}
