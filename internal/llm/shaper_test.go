package llm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sashabaranov/go-openai"

	redditJSON "github.com/meriley/reddit-spy/internal/redditJSON"
)

type fakeCompleter struct {
	req      openai.ChatCompletionRequest
	response string
	err      error
	calls    int
}

func (f *fakeCompleter) CreateChatCompletion(_ context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	f.req = req
	f.calls++
	if f.err != nil {
		return openai.ChatCompletionResponse{}, f.err
	}
	return openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{
			{Message: openai.ChatCompletionMessage{Content: f.response}},
		},
	}, nil
}

func TestShapeFresh_HappyPath(t *testing.T) {
	f := &fakeCompleter{response: `{"title":"Weekly Metalcore drop","summary":"A thread rounding up this week's singles."}`}
	s := NewShaper(f, Config{Model: "test-model"})

	out, err := s.ShapeFresh(context.Background(), FreshInput{
		Post: &redditJSON.RedditPost{
			ID:        "abc",
			Author:    "sink_or_swim1",
			Title:     "Weekly Release Thread April 17th, 2026",
			Selftext:  "**Singles/ICYMI**\n\nIrken Armada - Nail In The Coffin",
			Subreddit: "Metalcore",
		},
		RuleID:       2,
		RuleTargetID: "title",
		RuleExact:    false,
	})
	if err != nil {
		t.Fatalf("ShapeFresh: %v", err)
	}
	if out.Title != "Weekly Metalcore drop" {
		t.Errorf("title = %q, want %q", out.Title, "Weekly Metalcore drop")
	}
	if !strings.Contains(out.Summary, "singles") {
		t.Errorf("summary missing expected text: %q", out.Summary)
	}
	if f.req.Model != "test-model" {
		t.Errorf("request model = %q, want test-model", f.req.Model)
	}
	if len(f.req.Messages) != 2 {
		t.Fatalf("expected 2 messages (system+user), got %d", len(f.req.Messages))
	}
	if f.req.Messages[0].Role != openai.ChatMessageRoleSystem {
		t.Errorf("first message role = %q, want system", f.req.Messages[0].Role)
	}
	if !strings.Contains(f.req.Messages[1].Content, "r/Metalcore") {
		t.Errorf("user prompt missing subreddit reference")
	}
	if !strings.Contains(f.req.Messages[1].Content, "Rule #"+""+"") && !strings.Contains(f.req.Messages[1].Content, "rule:     #2") {
		t.Errorf("user prompt missing rule reference: %q", f.req.Messages[1].Content)
	}
}

func TestShapeUpdate_IncludesPriorNarrative(t *testing.T) {
	f := &fakeCompleter{response: `{"title":"Digest grows","summary":"Two threads this week."}`}
	s := NewShaper(f, Config{Model: "m"})

	out, err := s.ShapeUpdate(context.Background(), UpdateInput{
		PriorTitle:     "Prior title",
		PriorSummary:   "Prior narrative text.",
		PriorPostCount: 1,
		NewPost: &redditJSON.RedditPost{
			ID: "xyz", Author: "elderemothings",
			Title: "Weekly New Releases - April 10, 2026", Selftext: "Album list",
			Subreddit: "poppunkers",
		},
		NewRuleID: 3, NewRuleTargetID: "title", NewRuleExact: false,
	})
	if err != nil {
		t.Fatalf("ShapeUpdate: %v", err)
	}
	if out.Title != "Digest grows" {
		t.Errorf("title = %q", out.Title)
	}
	body := f.req.Messages[1].Content
	if !strings.Contains(body, "Prior narrative text.") {
		t.Errorf("update prompt missing prior narrative: %q", body)
	}
	if !strings.Contains(body, "posts so far: 1") {
		t.Errorf("update prompt missing prior count: %q", body)
	}
	if !strings.Contains(body, "r/poppunkers") {
		t.Errorf("update prompt missing new subreddit")
	}
}

func TestShape_SummaryClippedToBudget(t *testing.T) {
	long := strings.Repeat("a", SummaryCharBudget+200)
	f := &fakeCompleter{response: `{"title":"T","summary":"` + long + `"}`}
	s := NewShaper(f, Config{Model: "m"})

	out, err := s.ShapeFresh(context.Background(), FreshInput{
		Post: &redditJSON.RedditPost{Subreddit: "x"},
	})
	if err != nil {
		t.Fatalf("ShapeFresh: %v", err)
	}
	if n := len([]rune(out.Summary)); n != SummaryCharBudget {
		t.Errorf("summary rune count = %d, want %d", n, SummaryCharBudget)
	}
}

func TestShape_TitleClippedToLimit(t *testing.T) {
	longTitle := strings.Repeat("t", 200)
	f := &fakeCompleter{response: `{"title":"` + longTitle + `","summary":"ok"}`}
	s := NewShaper(f, Config{Model: "m"})

	out, err := s.ShapeFresh(context.Background(), FreshInput{
		Post: &redditJSON.RedditPost{Subreddit: "x"},
	})
	if err != nil {
		t.Fatalf("ShapeFresh: %v", err)
	}
	if n := len([]rune(out.Title)); n != 120 {
		t.Errorf("title rune count = %d, want 120", n)
	}
}

func TestShape_RejectsEmptyFields(t *testing.T) {
	f := &fakeCompleter{response: `{"title":"","summary":"x"}`}
	s := NewShaper(f, Config{Model: "m"})

	_, err := s.ShapeFresh(context.Background(), FreshInput{Post: &redditJSON.RedditPost{}})
	if err == nil {
		t.Fatal("expected error for empty title, got nil")
	}
}

func TestShape_StripsCodeFences(t *testing.T) {
	fenced := "```json\n{\"title\":\"Fenced\",\"summary\":\"body\"}\n```"
	f := &fakeCompleter{response: fenced}
	s := NewShaper(f, Config{Model: "m"})

	out, err := s.ShapeFresh(context.Background(), FreshInput{Post: &redditJSON.RedditPost{}})
	if err != nil {
		t.Fatalf("ShapeFresh: %v", err)
	}
	if out.Title != "Fenced" || out.Summary != "body" {
		t.Errorf("got %+v", out)
	}
}

func TestShape_PropagatesTransportError(t *testing.T) {
	f := &fakeCompleter{err: errors.New("boom")}
	s := NewShaper(f, Config{Model: "m"})

	_, err := s.ShapeFresh(context.Background(), FreshInput{Post: &redditJSON.RedditPost{}})
	if err == nil {
		t.Fatal("expected transport error to propagate")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want contains 'boom'", err)
	}
}

func TestShape_RejectsNilPost(t *testing.T) {
	f := &fakeCompleter{}
	s := NewShaper(f, Config{Model: "m"})

	if _, err := s.ShapeFresh(context.Background(), FreshInput{}); err == nil {
		t.Error("ShapeFresh with nil Post: expected error")
	}
	if _, err := s.ShapeUpdate(context.Background(), UpdateInput{}); err == nil {
		t.Error("ShapeUpdate with nil NewPost: expected error")
	}
	if f.calls != 0 {
		t.Errorf("transport should not be called when inputs are invalid; calls=%d", f.calls)
	}
}

func TestConfigFromEnv_MissingBase(t *testing.T) {
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "m")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error when LLM_BASE_URL missing")
	}
}

func TestConfigFromEnv_InvalidTimeout(t *testing.T) {
	t.Setenv(EnvBaseURL, "http://x")
	t.Setenv(EnvModel, "m")
	t.Setenv(EnvTimeout, "not-a-duration")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error when LLM_TIMEOUT malformed")
	}
}

func TestConfigFromEnv_DefaultsAndAPIKeyFallback(t *testing.T) {
	t.Setenv(EnvBaseURL, "http://x/v1")
	t.Setenv(EnvModel, "Qwen/Qwen3-14B-AWQ")
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvTimeout, "")
	t.Setenv(EnvTone, "")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.APIKey != "EMPTY" {
		t.Errorf("APIKey fallback = %q, want EMPTY", cfg.APIKey)
	}
	if cfg.Timeout != DefaultTimeout {
		t.Errorf("timeout = %v, want default %v", cfg.Timeout, DefaultTimeout)
	}
}
