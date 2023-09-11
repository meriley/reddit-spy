package database

import "time"

// Rule Document
type (
	Rule struct {
		Target   string `bson:"target,omitempty"`
		Exact    bool   `bson:"exact,omitempty"`
		TargetId string `bson:"targetId,omitempty"`
	}

	RuleChannel struct {
		Rules []*Rule `bson:"rules,omitempty"`
	}

	RuleServer struct {
		Channels map[string]*RuleChannel `bson:"channels,omitempty"`
	}

	RulesDocument struct {
		ID      string                 `bson:"id,omitempty"`
		Servers map[string]*RuleServer `bson:"servers,omitempty"`
	}
)

// Notification Document
type (
	NotificationDocument struct {
		ID          string    `bson:"id,omitempty"`
		ServerID    string    `bson:"serverId,omitempty"`
		ChannelID   string    `bson:"channelId,omitempty"`
		SubredditID string    `bson:"subredditId,omitempty"`
		NotifiedAt  time.Time `bson:"notifiedAt,omitempty"`
	}
)
