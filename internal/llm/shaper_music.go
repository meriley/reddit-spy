package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sashabaranov/go-openai"

	redditJSON "github.com/meriley/reddit-spy/internal/redditJSON"
)

// MusicEntry is one extracted release from a Reddit music-thread body.
// The bot stores these in rolling_posts.entries as a JSON array.
type MusicEntry struct {
	Artist       string `json:"artist"`
	Title        string `json:"title"`
	Kind         string `json:"kind"` // "single" | "album" | "ep"
	SourcePostID string `json:"source_post_id"`
	// Listeners is the Last.fm monthly-listener count, looked up post-
	// extraction. Zero means "unknown / not looked up yet"; the renderer
	// uses this only as a popularity tiebreaker and never requires it.
	Listeners int `json:"listeners,omitempty"`
}

// MusicInput drives a single ShapeMusic call. Pass the existing entries via
// KnownKeys so the model can dedupe against prior digest state — the shaper
// only returns NEW entries.
type MusicInput struct {
	Post         *redditJSON.RedditPost
	KnownEntries []MusicEntry
	RuleID       int
	RuleTargetID string
	RuleExact    bool
}

// ShapeMusic asks the LLM to pull `{artist, title, kind}` entries out of the
// post body. Returns only entries whose normalized (artist, title, kind) keys
// are NOT already in KnownEntries. On any LLM error the shaper returns an
// error — callers should fall back to leaving the digest unchanged rather
// than synthesising entries.
func (s *Shaper) ShapeMusic(ctx context.Context, in MusicInput) ([]MusicEntry, error) {
	if in.Post == nil {
		return nil, errors.New("llm.ShapeMusic: Post is nil")
	}

	prompt := promptMusicExtract(in)
	req := openai.ChatCompletionRequest{
		Model:       s.cfg.Model,
		Temperature: 0.1,
		// Cap output so vLLM doesn't drift and blow the http timeout on a
		// runaway completion. 3000 tokens fits ~100 JSON entries, which is
		// more than any weekly-release thread produces in practice.
		MaxTokens: 3000,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPromptMusic},
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	}
	resp, err := s.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("llm chat completion: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, errors.New("llm returned no choices")
	}
	raw := stripJSONFences(resp.Choices[0].Message.Content)

	// The model must return a JSON object: `{"entries": [...]}`. A few models
	// return the bare array, so accept both.
	var obj struct {
		Entries []MusicEntry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		var arr []MusicEntry
		if err2 := json.Unmarshal([]byte(raw), &arr); err2 != nil {
			return nil, fmt.Errorf("parse llm music json: %w (raw=%.200s)", err, raw)
		}
		obj.Entries = arr
	}

	known := make(map[string]struct{}, len(in.KnownEntries))
	for _, e := range in.KnownEntries {
		known[MusicDedupeKey(e)] = struct{}{}
	}

	out := make([]MusicEntry, 0, len(obj.Entries))
	for _, e := range obj.Entries {
		e.Artist = strings.TrimSpace(e.Artist)
		e.Title = strings.TrimSpace(e.Title)
		e.Kind = strings.ToLower(strings.TrimSpace(e.Kind))
		if e.Kind == "" {
			e.Kind = "single"
		}
		if e.Kind != "single" && e.Kind != "album" && e.Kind != "ep" {
			e.Kind = "single"
		}
		if e.Artist == "" || e.Title == "" {
			continue
		}
		key := MusicDedupeKey(e)
		if _, seen := known[key]; seen {
			continue
		}
		known[key] = struct{}{}
		e.SourcePostID = in.Post.ID
		out = append(out, e)
	}
	return out, nil
}

// MusicDedupeKey returns the normalized identity of a music entry for dedupe
// across days and threads. Case-folded, trimmed, parenthetical `(feat. ...)`
// fragments stripped from the title.
func MusicDedupeKey(e MusicEntry) string {
	artist := strings.ToLower(strings.TrimSpace(e.Artist))
	title := normalizeMusicTitle(e.Title)
	kind := strings.ToLower(strings.TrimSpace(e.Kind))
	return artist + "|" + title + "|" + kind
}

func normalizeMusicTitle(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	// Drop any " (feat. ..." or " feat. ..." suffix for dedupe purposes only.
	for _, marker := range []string{" (feat.", " (ft.", " feat.", " ft."} {
		if idx := strings.Index(t, marker); idx >= 0 {
			t = t[:idx]
			break
		}
	}
	return strings.TrimSpace(t)
}

func stripJSONFences(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = stripThinkBlock(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	return strings.TrimSpace(raw)
}

// stripThinkBlock removes any leading <think>…</think> reasoning block that
// Qwen3 and similar models emit when they haven't received the /no_think
// directive. If the </think> close tag is missing (model got cut off), drop
// everything up through the first `{` or `[` which is where the real payload
// starts for the digest shapers.
func stripThinkBlock(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "<think>") {
		return s
	}
	if idx := strings.Index(s, "</think>"); idx >= 0 {
		return strings.TrimSpace(s[idx+len("</think>"):])
	}
	// Close tag missing — fall back to first JSON delimiter.
	if i := strings.IndexAny(s, "{["); i >= 0 {
		return s[i:]
	}
	return s
}
