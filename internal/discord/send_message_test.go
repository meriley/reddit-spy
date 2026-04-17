package discord

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	ctxpkg "github.com/meriley/reddit-spy/internal/context"
	dbstore "github.com/meriley/reddit-spy/internal/dbstore"
	"github.com/meriley/reddit-spy/internal/evaluator"
	"github.com/meriley/reddit-spy/internal/llm"
	redditJSON "github.com/meriley/reddit-spy/internal/redditJSON"
	"github.com/meriley/reddit-spy/redditDiscordBot"
)

// ---------- fakes ----------

type fakeStore struct {
	mu                sync.Mutex
	notificationCount map[string]int
	rolling           []*dbstore.RollingPost
	nextRollingID     int
	nowFn             func() time.Time // matches the Client's injected clock when set
	channels          map[int]*dbstore.DiscordChannel
	subreddits        map[string]*dbstore.Subreddit
	notifyCalls       int
	upsertCalls       int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		notificationCount: map[string]int{},
		channels:          map[int]*dbstore.DiscordChannel{1: {ID: 1, ExternalID: "ext-chan-1"}},
		subreddits:        map[string]*dbstore.Subreddit{"Metalcore": {ID: 10, ExternalID: "metalcore"}},
		nowFn:             time.Now,
	}
}

func (s *fakeStore) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

func notifKey(postID, channelID, ruleID int) string {
	return fmt.Sprintf("n|%d|%d|%d", postID, channelID, ruleID)
}

func (s *fakeStore) GetNotificationCount(_ context.Context, postID, channelID, ruleID int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notificationCount[notifKey(postID, channelID, ruleID)], nil
}
func (s *fakeStore) InsertNotification(_ context.Context, postID, channelID, ruleID int) (*dbstore.Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notificationCount[notifKey(postID, channelID, ruleID)]++
	s.notifyCalls++
	return &dbstore.Notification{ID: 1, PostID: postID, ChannelID: channelID, RuleID: ruleID}, nil
}
func (s *fakeStore) GetDiscordChannel(_ context.Context, id int) (*dbstore.DiscordChannel, error) {
	ch, ok := s.channels[id]
	if !ok {
		return nil, errors.New("no channel")
	}
	return ch, nil
}
func (s *fakeStore) GetSubredditByExternalID(_ context.Context, name string) (*dbstore.Subreddit, error) {
	sr, ok := s.subreddits[name]
	if !ok {
		return nil, errors.New("no subreddit")
	}
	return sr, nil
}
func (s *fakeStore) GetActiveRollingPost(_ context.Context, channelID int, mode string, windowHours int) (*dbstore.RollingPost, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mode == "" {
		mode = dbstore.ModeNarrative
	}
	now := s.now()
	var latest *dbstore.RollingPost
	for _, rp := range s.rolling {
		if rp.ChannelID != channelID {
			continue
		}
		rpMode := rp.Mode
		if rpMode == "" {
			rpMode = dbstore.ModeNarrative
		}
		if rpMode != mode {
			continue
		}
		closesAt := rp.WindowStart.Add(time.Duration(windowHours) * time.Hour)
		if !closesAt.After(now) {
			continue
		}
		if latest == nil || rp.WindowStart.After(latest.WindowStart) {
			latest = rp
		}
	}
	if latest == nil {
		return nil, nil
	}
	// Return a copy so callers can't mutate the store's backing slice.
	cp := *latest
	return &cp, nil
}

func (s *fakeStore) UpsertRollingPost(_ context.Context, rp dbstore.RollingPost) (*dbstore.RollingPost, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upsertCalls++
	if rp.ID == 0 {
		s.nextRollingID++
		rp.ID = s.nextRollingID
		if rp.WindowStart.IsZero() {
			rp.WindowStart = s.now()
		}
		stored := rp
		s.rolling = append(s.rolling, &stored)
		return &stored, nil
	}
	for i, existing := range s.rolling {
		if existing.ID != rp.ID {
			continue
		}
		// Preserve identity fields the real UPDATE doesn't touch.
		rp.WindowStart = existing.WindowStart
		rp.DayLocal = existing.DayLocal
		stored := rp
		s.rolling[i] = &stored
		return &stored, nil
	}
	return nil, fmt.Errorf("fakeStore: rolling_post id=%d not found", rp.ID)
}

// --- Store interface no-ops (not exercised by SendMessage) ---
func (s *fakeStore) InsertDiscordServer(_ context.Context, _ string) (*dbstore.DiscordServer, error) {
	return nil, nil
}
func (s *fakeStore) InsertDiscordChannel(_ context.Context, _ string, _ int) (*dbstore.DiscordChannel, error) {
	return nil, nil
}
func (s *fakeStore) InsertSubreddit(_ context.Context, _ string) (*dbstore.Subreddit, error) {
	return nil, nil
}
func (s *fakeStore) InsertRule(_ context.Context, _ dbstore.Rule) (*dbstore.Rule, error) {
	return nil, nil
}
func (s *fakeStore) InsertPost(_ context.Context, _ string) (*dbstore.Post, error) {
	return nil, nil
}
func (s *fakeStore) GetDiscordServerByExternalID(_ context.Context, _ string) (*dbstore.DiscordServer, error) {
	return nil, nil
}
func (s *fakeStore) GetDiscordChannelByExternalID(_ context.Context, extID string) (*dbstore.DiscordChannel, error) {
	for _, ch := range s.channels {
		if ch.ExternalID == extID {
			return ch, nil
		}
	}
	return nil, errors.New("no channel by external id")
}
func (s *fakeStore) GetRules(_ context.Context, _ int) ([]*dbstore.Rule, error)    { return nil, nil }
func (s *fakeStore) GetSubreddits(_ context.Context) ([]*dbstore.Subreddit, error) { return nil, nil }
func (s *fakeStore) GetRulesByChannel(_ context.Context, _ string) ([]*dbstore.RuleDetail, error) {
	return nil, nil
}
func (s *fakeStore) GetRuleByID(_ context.Context, _ int) (*dbstore.RuleDetail, error) {
	return nil, nil
}
func (s *fakeStore) DeleteRule(_ context.Context, _ int) error                   { return nil }
func (s *fakeStore) UpdateRule(_ context.Context, _ int, _ string, _ bool) error { return nil }
func (s *fakeStore) UpdateRuleMode(_ context.Context, _ int, _ string) error     { return nil }
func (s *fakeStore) UpdateRuleWindowHours(_ context.Context, _ int, _ int) error { return nil }
func (s *fakeStore) GetLastfmListeners(_ context.Context, _ string) (int, time.Time, bool, error) {
	return 0, time.Time{}, false, nil
}
func (s *fakeStore) UpsertLastfmListeners(_ context.Context, _ string, _ int) error { return nil }
func (s *fakeStore) GetLastfmArtist(_ context.Context, _ string) (int, []string, time.Time, bool, error) {
	return 0, nil, time.Time{}, false, nil
}
func (s *fakeStore) UpsertLastfmArtist(_ context.Context, _ string, _ int, _ []string) error {
	return nil
}
func (s *fakeStore) GetPipedVideo(_ context.Context, _ string) (string, time.Time, bool, error) {
	return "", time.Time{}, false, nil
}
func (s *fakeStore) UpsertPipedVideo(_ context.Context, _, _ string) error { return nil }
func (s *fakeStore) GetQobuzAlbum(_ context.Context, _ string) (string, time.Time, bool, error) {
	return "", time.Time{}, false, nil
}
func (s *fakeStore) UpsertQobuzAlbum(_ context.Context, _, _ string) error { return nil }

// ---------- fake sender ----------

type fakeSender struct {
	sendCalls int
	editCalls int
	sends     []*discordgo.MessageSend
	edits     []*discordgo.MessageEdit
	nextMsgID string
	editErr   error
}

func (f *fakeSender) ChannelMessageSendComplex(_ string, data *discordgo.MessageSend, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.sendCalls++
	f.sends = append(f.sends, data)
	id := f.nextMsgID
	if id == "" {
		id = "msg-auto"
	}
	return &discordgo.Message{ID: id}, nil
}

func (f *fakeSender) ChannelMessageEditComplex(m *discordgo.MessageEdit, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.editCalls++
	f.edits = append(f.edits, m)
	if f.editErr != nil {
		err := f.editErr
		f.editErr = nil // clear so a fallback send doesn't loop
		return nil, err
	}
	return &discordgo.Message{ID: m.ID}, nil
}

// Thread-aware MessageSender stubs. Narrative-mode tests don't exercise
// these paths; they're here to satisfy the interface. Music-mode tests
// (added separately) drive them.
func (f *fakeSender) ChannelMessageDelete(_, _ string, _ ...discordgo.RequestOption) error {
	return nil
}

func (f *fakeSender) MessageThreadStart(_, _ string, name string, _ int, _ ...discordgo.RequestOption) (*discordgo.Channel, error) {
	return &discordgo.Channel{ID: "thread-" + name}, nil
}

func (f *fakeSender) ChannelEdit(_ string, _ *discordgo.ChannelEdit, _ ...discordgo.RequestOption) (*discordgo.Channel, error) {
	return &discordgo.Channel{}, nil
}

// ---------- fake shaper ----------

type fakeShaper struct {
	freshCalls  int
	updateCalls int
	freshOut    llm.Output
	updateOut   llm.Output
}

func (s *fakeShaper) ShapeFresh(_ ctxpkg.Ctx, _ llm.FreshInput) (llm.Output, error) {
	s.freshCalls++
	return s.freshOut, nil
}
func (s *fakeShaper) ShapeUpdate(_ ctxpkg.Ctx, _ llm.UpdateInput) (llm.Output, error) {
	s.updateCalls++
	return s.updateOut, nil
}
func (s *fakeShaper) ShapeMusic(_ ctxpkg.Ctx, _ llm.MusicInput) ([]llm.MusicEntry, error) {
	return nil, nil
}

// ---------- helpers ----------

func appCtx(t *testing.T) ctxpkg.Ctx {
	t.Helper()
	return ctxpkg.New(context.Background())
}

func buildClient(store *fakeStore, sender MessageSender, shaper Shaper, now func() time.Time) *Client {
	// Keep the store's clock in sync with the Client's injected clock so
	// GetActiveRollingPost's window math sees the same "now" SendMessage does.
	store.nowFn = now
	bot := &redditDiscordBot.RedditDiscordBot{Store: store}
	return NewForTest(
		ctxpkg.New(context.Background()),
		bot,
		WithSender(sender),
		WithShaperInterface(shaper),
		WithNow(now),
	)
}

func newMatch(postID int, ruleID int, reddit *redditJSON.RedditPost) *evaluator.MatchingEvaluationResult {
	return &evaluator.MatchingEvaluationResult{
		ChannelID: 1,
		RuleID:    ruleID,
		PostID:    postID,
		Post:      reddit,
		Rule:      &dbstore.Rule{ID: ruleID, TargetID: "title", Exact: false},
	}
}

// ---------- tests ----------

func TestSendMessage_FirstMatchSends(t *testing.T) {
	store := newFakeStore()
	sender := &fakeSender{nextMsgID: "msg-1"}
	shaper := &fakeShaper{freshOut: llm.Output{Title: "Day 1 digest", Summary: "one post so far"}}
	now := func() time.Time { return time.Date(2026, 4, 16, 14, 0, 0, 0, time.UTC) }
	c := buildClient(store, sender, shaper, now)

	err := c.SendMessage(appCtx(t), newMatch(100, 2, &redditJSON.RedditPost{
		ID: "abc", Author: "u1", Subreddit: "Metalcore", Title: "weekly thread",
		Selftext: "bands", Score: 2, NumComments: 1,
	}))
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if sender.sendCalls != 1 || sender.editCalls != 0 {
		t.Errorf("send=%d edit=%d, want send=1 edit=0", sender.sendCalls, sender.editCalls)
	}
	if shaper.freshCalls != 1 || shaper.updateCalls != 0 {
		t.Errorf("fresh=%d update=%d", shaper.freshCalls, shaper.updateCalls)
	}
	if store.notifyCalls != 1 || store.upsertCalls != 1 {
		t.Errorf("notify=%d upsert=%d", store.notifyCalls, store.upsertCalls)
	}
}

func TestSendMessage_SameDaySecondMatchEdits(t *testing.T) {
	store := newFakeStore()
	sender := &fakeSender{nextMsgID: "msg-1"}
	shaper := &fakeShaper{
		freshOut:  llm.Output{Title: "Day 1", Summary: "one"},
		updateOut: llm.Output{Title: "Day 1 v2", Summary: "two posts now"},
	}
	now := func() time.Time { return time.Date(2026, 4, 16, 14, 0, 0, 0, time.UTC) }
	c := buildClient(store, sender, shaper, now)

	first := newMatch(100, 2, &redditJSON.RedditPost{ID: "p1", Subreddit: "Metalcore", Title: "t1", Score: 2, NumComments: 1})
	second := newMatch(101, 2, &redditJSON.RedditPost{ID: "p2", Subreddit: "Metalcore", Title: "t2", Score: 5, NumComments: 3})

	if err := c.SendMessage(appCtx(t), first); err != nil {
		t.Fatalf("first SendMessage: %v", err)
	}
	if err := c.SendMessage(appCtx(t), second); err != nil {
		t.Fatalf("second SendMessage: %v", err)
	}
	if sender.sendCalls != 1 {
		t.Errorf("sendCalls=%d, want 1", sender.sendCalls)
	}
	if sender.editCalls != 1 {
		t.Errorf("editCalls=%d, want 1", sender.editCalls)
	}
	if shaper.freshCalls != 1 || shaper.updateCalls != 1 {
		t.Errorf("fresh=%d update=%d, want 1/1", shaper.freshCalls, shaper.updateCalls)
	}
	if len(sender.edits) != 1 {
		t.Fatalf("expected 1 edit captured")
	}
	if sender.edits[0].ID != "msg-1" {
		t.Errorf("edit targeted %q, want msg-1", sender.edits[0].ID)
	}
}

// TestSendMessage_PastWindowSendsNewMessage pins the new window-based
// grouping: a match arriving AFTER window_hours have elapsed since the first
// match's window_start opens a new rolling_post (new Discord send) instead
// of editing the old one.
func TestSendMessage_PastWindowSendsNewMessage(t *testing.T) {
	store := newFakeStore()
	sender := &fakeSender{nextMsgID: "msg-1"}
	shaper := &fakeShaper{
		freshOut:  llm.Output{Title: "T", Summary: "S"},
		updateOut: llm.Output{Title: "U", Summary: "u"},
	}
	t0 := time.Date(2026, 4, 16, 14, 0, 0, 0, time.UTC)
	// Default rule.WindowHours is 0 in the fake → effective default is 72h.
	// Second match 80h later is past the window.
	t1 := t0.Add(80 * time.Hour)
	clock := t0
	now := func() time.Time { return clock }
	c := buildClient(store, sender, shaper, now)

	if err := c.SendMessage(appCtx(t), newMatch(100, 2, &redditJSON.RedditPost{ID: "p1", Subreddit: "Metalcore", Title: "t1"})); err != nil {
		t.Fatalf("first SendMessage: %v", err)
	}
	sender.nextMsgID = "msg-2"
	clock = t1
	if err := c.SendMessage(appCtx(t), newMatch(101, 2, &redditJSON.RedditPost{ID: "p2", Subreddit: "Metalcore", Title: "t2"})); err != nil {
		t.Fatalf("post-window SendMessage: %v", err)
	}
	if sender.sendCalls != 2 {
		t.Errorf("sendCalls=%d, want 2 (one per window)", sender.sendCalls)
	}
	if sender.editCalls != 0 {
		t.Errorf("editCalls=%d, want 0 across a window boundary", sender.editCalls)
	}
	if shaper.freshCalls != 2 {
		t.Errorf("freshCalls=%d, want 2", shaper.freshCalls)
	}
}

func TestSendMessage_EditFallsBackWhenMessageDeleted(t *testing.T) {
	store := newFakeStore()
	sender := &fakeSender{nextMsgID: "msg-1"}
	shaper := &fakeShaper{
		freshOut:  llm.Output{Title: "T", Summary: "S"},
		updateOut: llm.Output{Title: "U", Summary: "u"},
	}
	now := func() time.Time { return time.Date(2026, 4, 16, 14, 0, 0, 0, time.UTC) }
	c := buildClient(store, sender, shaper, now)

	if err := c.SendMessage(appCtx(t), newMatch(100, 2, &redditJSON.RedditPost{ID: "p1", Subreddit: "Metalcore", Title: "t1"})); err != nil {
		t.Fatalf("first SendMessage: %v", err)
	}
	// Simulate: a human deleted the Discord message. Next edit returns 404.
	sender.nextMsgID = "msg-2"
	sender.editErr = &discordgo.RESTError{
		Response: &http.Response{StatusCode: 404},
		Message:  &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownMessage},
	}
	if err := c.SendMessage(appCtx(t), newMatch(101, 2, &redditJSON.RedditPost{ID: "p2", Subreddit: "Metalcore", Title: "t2"})); err != nil {
		t.Fatalf("second SendMessage: %v", err)
	}
	if sender.editCalls != 1 {
		t.Errorf("editCalls=%d, want 1 (attempted edit)", sender.editCalls)
	}
	if sender.sendCalls != 2 {
		t.Errorf("sendCalls=%d, want 2 (fresh + fallback)", sender.sendCalls)
	}
	// After fallback, stored message_id should point at the replacement.
	rp, _ := store.GetActiveRollingPost(context.Background(), 1, dbstore.ModeNarrative, 72)
	if rp == nil {
		t.Fatal("rolling post row missing after fallback")
		return
	}
	if len(rp.DiscordMessageIDs) == 0 || rp.DiscordMessageIDs[0] != "msg-2" {
		t.Errorf("DiscordMessageIDs=%v, want [msg-2]", rp.DiscordMessageIDs)
	}
}

func TestSendMessage_DedupeShortCircuits(t *testing.T) {
	store := newFakeStore()
	sender := &fakeSender{}
	shaper := &fakeShaper{}
	now := func() time.Time { return time.Date(2026, 4, 16, 14, 0, 0, 0, time.UTC) }
	c := buildClient(store, sender, shaper, now)

	// Pre-seed a notification for (post=100, channel=1, rule=2).
	if _, err := store.InsertNotification(context.Background(), 100, 1, 2); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := c.SendMessage(appCtx(t), newMatch(100, 2, &redditJSON.RedditPost{ID: "p1", Subreddit: "Metalcore"})); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if sender.sendCalls != 0 || sender.editCalls != 0 {
		t.Errorf("sender should not be called on dedupe hit (send=%d edit=%d)", sender.sendCalls, sender.editCalls)
	}
	if shaper.freshCalls != 0 || shaper.updateCalls != 0 {
		t.Errorf("shaper should not be called on dedupe hit (fresh=%d update=%d)", shaper.freshCalls, shaper.updateCalls)
	}
}

// TestSendMessage_WindowBoundary_CrossingClockMidnight pins the new window
// semantics: crossing Phoenix midnight WITHIN the same 72h window should
// still edit the existing digest (not open a new one). Under the old
// day_local bucketing this would have been a "new day, new digest" event.
func TestSendMessage_WindowBoundary_CrossingClockMidnight(t *testing.T) {
	store := newFakeStore()
	sender := &fakeSender{nextMsgID: "msg-1"}
	shaper := &fakeShaper{
		freshOut:  llm.Output{Title: "T", Summary: "S"},
		updateOut: llm.Output{Title: "U", Summary: "u2"},
	}
	// 2026-04-16 20:00 Phoenix = 2026-04-17 03:00 UTC
	t0 := time.Date(2026, 4, 17, 3, 0, 0, 0, time.UTC)
	// 10h later = 2026-04-17 13:00 UTC = 2026-04-17 06:00 Phoenix (next local day)
	t1 := t0.Add(10 * time.Hour)
	clock := t0
	c := buildClient(store, sender, shaper, func() time.Time { return clock })

	if err := c.SendMessage(appCtx(t), newMatch(100, 2, &redditJSON.RedditPost{ID: "p1", Subreddit: "Metalcore", Title: "t1"})); err != nil {
		t.Fatalf("first SendMessage: %v", err)
	}
	clock = t1
	if err := c.SendMessage(appCtx(t), newMatch(101, 2, &redditJSON.RedditPost{ID: "p2", Subreddit: "Metalcore", Title: "t2"})); err != nil {
		t.Fatalf("second SendMessage: %v", err)
	}
	if sender.sendCalls != 1 || sender.editCalls != 1 {
		t.Errorf("send=%d edit=%d, want send=1 edit=1 (same window, same message)", sender.sendCalls, sender.editCalls)
	}

	// Still exactly one rolling_post row for this (channel, sub) in the
	// default 72h window.
	rp, _ := store.GetActiveRollingPost(context.Background(), 1, dbstore.ModeNarrative, 72)
	if rp == nil {
		t.Fatal("expected a rolling_posts row for the active 72h window")
		return
	}
	if len(rp.IncludedPostIDs) != 2 {
		t.Errorf("included_post_ids=%v, want 2 entries", rp.IncludedPostIDs)
	}
}

func TestSendMessage_NoShaperFallsBackToRawSelftext(t *testing.T) {
	store := newFakeStore()
	sender := &fakeSender{nextMsgID: "msg-1"}
	now := func() time.Time { return time.Date(2026, 4, 16, 14, 0, 0, 0, time.UTC) }
	c := buildClient(store, sender, nil, now)

	err := c.SendMessage(appCtx(t), newMatch(100, 2, &redditJSON.RedditPost{
		ID: "p1", Subreddit: "Metalcore", Title: "Weekly Release Thread", Selftext: "singles list",
	}))
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if sender.sendCalls != 1 {
		t.Fatalf("sendCalls=%d, want 1", sender.sendCalls)
	}
	got := sender.sends[0].Embeds[0]
	if got.Title != "Weekly Release Thread" {
		t.Errorf("title = %q, want raw post title", got.Title)
	}
	if got.Description != "singles list" {
		t.Errorf("description = %q, want raw selftext", got.Description)
	}
}
