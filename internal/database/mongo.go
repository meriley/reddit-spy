package database

import (
	context2 "context"
	"github.com/meriley/reddit-spy/internal/context"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"os"
	"strings"
	"time"
)

const (
	EnvMongoURI = "mongo.address"

	DBNAME                   = "reddit-discord-bot"
	DBSubredditCollection    = "subreddits"
	DBNotificationCollection = "notifications"
)

type DBInterface interface {
	GetSubreddits() (string, error)
	GetRulesDocument(subreddit string) (*RulesDocument, error)
	InsertRule(subreddit string) error
}

type DB struct {
	DBInterface
	Ctx     context.Ctx
	MongoDB *mongo.Client
}

func (d *DB) GetSubreddits() ([]string, error) {
	var result struct {
		ID string
	}

	cursor, err := d.MongoDB.Database(DBNAME).
		Collection(DBSubredditCollection).
		Find(d.Ctx.Context(), bson.D{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to find subreddits")
	}

	defer func(cursor *mongo.Cursor, ctx context2.Context) {
		if err := cursor.Close(ctx); err != nil {
			panic(err)
		}
	}(cursor, d.Ctx.Context())
	subreddits := make([]string, 0, cursor.RemainingBatchLength())

	for cursor.Next(d.Ctx.Context()) {
		err := cursor.Decode(&result)
		if err != nil {
			return nil, errors.Wrap(err, "failed to decode mongodb response")
		}
		subreddits = append(subreddits, result.ID)
	}
	return subreddits, nil
}

func (d *DB) GetRulesDocument(subreddit string) (*RulesDocument, error) {
	var result RulesDocument
	err := d.MongoDB.Database(DBNAME).
		Collection(DBSubredditCollection).
		FindOne(d.Ctx.Context(), bson.D{{Key: "id", Value: strings.ToLower(subreddit)}}).
		Decode(&result)
	if err != nil {
		return nil, errors.Wrap(err, "failed to find rules document")
	}
	return &result, nil
}

func (d *DB) InsertRule(subreddit, serverID, channelID string, rule *Rule) error {
	// Define paths
	basePath := "servers." + serverID
	channelPath := basePath + ".channels." + channelID
	rulePath := channelPath + ".rules"

	opts := options.Update().SetUpsert(true)
	filter := bson.D{{"id", strings.ToLower(subreddit)}}
	update := bson.M{"$push": bson.M{rulePath: rule}}

	_, err := d.MongoDB.Database(DBNAME).Collection(DBSubredditCollection).UpdateOne(d.Ctx.Context(), filter, update, opts)
	if err != nil && strings.Contains(err.Error(), "would create a conflict at") {
		// Initialize paths
		initPaths := bson.M{
			basePath: &RuleServer{
				Channels: map[string]*RuleChannel{
					channelID: {Rules: []*Rule{}},
				}},
		}

		_, initErr := d.MongoDB.Database(DBNAME).Collection(DBSubredditCollection).UpdateOne(d.Ctx.Context(), filter, bson.M{"$set": initPaths}, opts)
		if initErr != nil {
			return errors.Wrap(initErr, "failed to initialize paths")
		}

		// Retry pushing the rule
		_, err = d.MongoDB.Database(DBNAME).Collection(DBSubredditCollection).UpdateOne(d.Ctx.Context(), filter, update, opts)
	}

	if err != nil {
		return errors.Wrap(err, "failed to upsert the document")
	}

	return nil
}

func (d *DB) GetNotification(postID string, serverID string, channelID string) *NotificationDocument {
	var document NotificationDocument
	err := d.MongoDB.Database(DBNAME).
		Collection(DBNotificationCollection).
		FindOne(d.Ctx.Context(), bson.M{
			"id":        postID,
			"serverId":  serverID,
			"channelId": channelID}).
		Decode(&document)
	if err != nil {
		return nil
	}
	return &document
}

func (d *DB) InsertNotification(postId string, serverID string, channelID string, subredditID string) error {
	document := bson.M{
		"id":          postId,
		"serverId":    serverID,
		"channelId":   channelID,
		"subredditId": strings.ToLower(subredditID),
		"notifiedAt":  time.Now(),
	}
	_, err := d.MongoDB.Database(DBNAME).Collection(DBNotificationCollection).InsertOne(d.Ctx.Context(), document)
	if err != nil {
		return errors.Wrap(err, "failed to upsert the document")
	}
	return nil
}

func New(ctx context.Ctx) (*DB, error) {
	uri := os.Getenv(EnvMongoURI)
	client, err := mongo.Connect(ctx.Context(), options.Client().ApplyURI(uri))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create mongodb client")
	}
	return &DB{
		Ctx:     ctx,
		MongoDB: client,
	}, nil
}
