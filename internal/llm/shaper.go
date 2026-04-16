package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/sashabaranov/go-openai"

	redditJSON "github.com/meriley/reddit-spy/internal/redditJSON"
)

// Mode selects which prompt variant the shaper uses.
type Mode int

const (
	ModeFresh Mode = iota
	ModeUpdate
)

// FreshInput is the payload for the first match of a (subreddit, day) pair.
type FreshInput struct {
	Post         *redditJSON.RedditPost
	RuleID       int
	RuleTargetID string
	RuleExact    bool
}

// UpdateInput is the payload for a subsequent match that edits an existing
// rolling digest.
type UpdateInput struct {
	PriorTitle      string
	PriorSummary    string
	PriorPostCount  int
	NewPost         *redditJSON.RedditPost
	NewRuleID       int
	NewRuleTargetID string
	NewRuleExact    bool
}

// Output is what the shaper returns to the Discord layer.
type Output struct {
	Title   string
	Summary string
}

// Shaper turns Reddit matches into Discord-ready narratives via an
// OpenAI-compatible chat-completions endpoint.
type Shaper struct {
	client ChatCompleter
	cfg    Config
}

// NewShaper returns a Shaper backed by the supplied ChatCompleter. The Config
// controls the target model and tone.
func NewShaper(client ChatCompleter, cfg Config) *Shaper {
	return &Shaper{client: client, cfg: cfg}
}

// ShapeFresh produces a narrative for the first match of a rolling digest.
func (s *Shaper) ShapeFresh(ctx context.Context, in FreshInput) (Output, error) {
	if in.Post == nil {
		return Output{}, errors.New("llm.ShapeFresh: Post is nil")
	}
	prompt := promptFresh(in, s.cfg.Tone, SummaryCharBudget)
	return s.complete(ctx, prompt)
}

// ShapeUpdate rewrites a rolling digest to absorb one additional match.
func (s *Shaper) ShapeUpdate(ctx context.Context, in UpdateInput) (Output, error) {
	if in.NewPost == nil {
		return Output{}, errors.New("llm.ShapeUpdate: NewPost is nil")
	}
	prompt := promptUpdate(in, s.cfg.Tone, SummaryCharBudget)
	return s.complete(ctx, prompt)
}

func (s *Shaper) complete(ctx context.Context, userPrompt string) (Output, error) {
	req := openai.ChatCompletionRequest{
		Model:       s.cfg.Model,
		Temperature: 0.2,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	}

	resp, err := s.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return Output{}, fmt.Errorf("llm chat completion: %w", err)
	}
	if len(resp.Choices) == 0 {
		return Output{}, errors.New("llm returned no choices")
	}

	return parseOutput(resp.Choices[0].Message.Content)
}

// parseOutput extracts {title, summary} from the model's JSON response,
// clips the summary to SummaryCharBudget runes, and normalizes whitespace in
// the title.
func parseOutput(raw string) (Output, error) {
	raw = strings.TrimSpace(raw)
	// Some models wrap JSON in a ```json code fence despite the response_format
	// directive; strip it defensively.
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var payload struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return Output{}, fmt.Errorf("parse llm json: %w", err)
	}
	if payload.Title == "" || payload.Summary == "" {
		return Output{}, errors.New("llm output missing title or summary")
	}

	return Output{
		Title:   clipRunes(quoteSingleLine(payload.Title), 120),
		Summary: clipRunes(payload.Summary, SummaryCharBudget),
	}, nil
}

func clipRunes(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes])
}
