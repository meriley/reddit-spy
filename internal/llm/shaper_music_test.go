package llm

import (
	"context"
	"strings"
	"testing"

	"github.com/sashabaranov/go-openai"

	redditJSON "github.com/meriley/reddit-spy/internal/redditJSON"
)

// multiCompleter returns responses[i] for the i-th call; empty string thereafter.
type multiCompleter struct {
	responses []string
	calls     int
	reqs      []openai.ChatCompletionRequest
}

func (m *multiCompleter) CreateChatCompletion(_ context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	m.reqs = append(m.reqs, req)
	resp := ""
	if m.calls < len(m.responses) {
		resp = m.responses[m.calls]
	}
	m.calls++
	return openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{
			{Message: openai.ChatCompletionMessage{Content: resp}},
		},
	}, nil
}

func TestShapeMusic_SingleChunk(t *testing.T) {
	f := &fakeCompleter{response: `{"entries":[{"artist":"Irken Armada","title":"Nail In The Coffin","kind":"single"}]}`}
	s := NewShaper(f, Config{Model: "m"})

	entries, err := s.ShapeMusic(context.Background(), MusicInput{
		Post: &redditJSON.RedditPost{ID: "p1", Selftext: "Irken Armada - Nail In The Coffin"},
	})
	if err != nil {
		t.Fatalf("ShapeMusic: %v", err)
	}
	if f.calls != 1 {
		t.Errorf("expected 1 LLM call, got %d", f.calls)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Artist != "Irken Armada" {
		t.Errorf("artist = %q, want Irken Armada", entries[0].Artist)
	}
	if entries[0].SourcePostID != "p1" {
		t.Errorf("SourcePostID = %q, want p1", entries[0].SourcePostID)
	}
}

func TestShapeMusic_NilPost(t *testing.T) {
	f := &fakeCompleter{}
	s := NewShaper(f, Config{Model: "m"})
	if _, err := s.ShapeMusic(context.Background(), MusicInput{}); err == nil {
		t.Fatal("expected error for nil Post")
	}
	if f.calls != 0 {
		t.Errorf("transport should not be called for nil Post; calls=%d", f.calls)
	}
}

func TestShapeMusic_KnownEntriesDeduped(t *testing.T) {
	// Model returns two entries; one is already known — expect only the new one.
	f := &fakeCompleter{response: `{"entries":[` +
		`{"artist":"Irken Armada","title":"Nail In The Coffin","kind":"single"},` +
		`{"artist":"The Maine","title":"Joy Next Door","kind":"album"}` +
		`]}`}
	s := NewShaper(f, Config{Model: "m"})

	known := []MusicEntry{{Artist: "Irken Armada", Title: "Nail In The Coffin", Kind: "single"}}
	entries, err := s.ShapeMusic(context.Background(), MusicInput{
		Post:         &redditJSON.RedditPost{ID: "p1", Selftext: "..."},
		KnownEntries: known,
	})
	if err != nil {
		t.Fatalf("ShapeMusic: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1 (known entry deduped)", len(entries))
	}
	if entries[0].Artist != "The Maine" {
		t.Errorf("got artist %q, want The Maine", entries[0].Artist)
	}
}

func TestShapeMusic_MultiChunkAccumulates(t *testing.T) {
	// Derive the fixed overhead so we can set ContextLimit just high enough to
	// fit one line per chunk, forcing the body to split across two calls.
	baseIn := MusicInput{Post: &redditJSON.RedditPost{}}
	emptyPrompt := promptMusicExtract(baseIn, "")
	fixedTokens := (len(systemPromptMusic) + len(emptyPrompt)) / charsPerToken

	// bodyBudget = available * charsPerToken where available = limit - headroom - fixedTokens.
	// Each line is ~25 chars; to fit exactly one line per call: bodyBudget = 25.
	// available = 25/3 ≈ 9  →  limit = 9 + contextHeadroom + fixedTokens + 1 (round up)
	limit := fixedTokens + contextHeadroom + (25 / charsPerToken) + 1

	mc := &multiCompleter{responses: []string{
		`{"entries":[{"artist":"Artist A","title":"Song X","kind":"single"}]}`,
		`{"entries":[{"artist":"Artist B","title":"Song Y","kind":"album"}]}`,
	}}
	s := NewShaper(mc, Config{Model: "m", ContextLimit: limit})

	// Two lines, each ~25 chars — second must spill into its own chunk.
	body := "Artist A - Song X (Single)\nArtist B - Song Y (Album)"
	entries, err := s.ShapeMusic(context.Background(), MusicInput{
		Post: &redditJSON.RedditPost{ID: "p1", Selftext: body},
	})
	if err != nil {
		t.Fatalf("ShapeMusic: %v", err)
	}
	if mc.calls != 2 {
		t.Errorf("expected 2 LLM calls for 2 chunks, got %d", mc.calls)
	}
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2", len(entries))
	}
}

func TestShapeMusic_CrossChunkDedup(t *testing.T) {
	// Chunk 2 model response echoes an entry already returned in chunk 1 —
	// it must not appear twice in the final output.
	baseIn := MusicInput{Post: &redditJSON.RedditPost{}}
	emptyPrompt := promptMusicExtract(baseIn, "")
	fixedTokens := (len(systemPromptMusic) + len(emptyPrompt)) / charsPerToken
	limit := fixedTokens + contextHeadroom + (25 / charsPerToken) + 1

	mc := &multiCompleter{responses: []string{
		`{"entries":[{"artist":"Artist A","title":"Song X","kind":"single"}]}`,
		// chunk 2 echoes A+X (already known) and adds B+Y
		`{"entries":[{"artist":"Artist A","title":"Song X","kind":"single"},{"artist":"Artist B","title":"Song Y","kind":"single"}]}`,
	}}
	s := NewShaper(mc, Config{Model: "m", ContextLimit: limit})

	body := "Artist A - Song X (Single)\nArtist B - Song Y (Single)"
	entries, err := s.ShapeMusic(context.Background(), MusicInput{
		Post: &redditJSON.RedditPost{ID: "p1", Selftext: body},
	})
	if err != nil {
		t.Fatalf("ShapeMusic: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2 (no cross-chunk dup)", len(entries))
	}
}

func TestTakeBodyChunk_FitsInSingle(t *testing.T) {
	s := NewShaper(&fakeCompleter{}, Config{Model: "m"})
	in := MusicInput{Post: &redditJSON.RedditPost{}}
	body := "Artist A - Song X\nArtist B - Song Y"
	chunk, rest := s.takeBodyChunk(in, body)
	if rest != "" {
		t.Errorf("expected no rest for small body, got %q", rest)
	}
	if chunk != body {
		t.Errorf("chunk = %q, want full body", chunk)
	}
}

func TestTakeBodyChunk_SplitsAtLineBoundary(t *testing.T) {
	s := NewShaper(&fakeCompleter{}, Config{Model: "m"})
	in := MusicInput{Post: &redditJSON.RedditPost{}}

	// Compute the exact body budget for this input so we can straddle the boundary.
	emptyPrompt := promptMusicExtract(in, "")
	fixedTokens := (len(systemPromptMusic) + len(emptyPrompt)) / charsPerToken
	available := DefaultContextLimit - contextHeadroom - fixedTokens
	bodyBudget := available * charsPerToken

	// line1 fits; line1+line2 overflows.
	line1 := strings.Repeat("a", bodyBudget-1)
	line2 := "overflow_line"
	body := line1 + "\n" + line2

	chunk, rest := s.takeBodyChunk(in, body)
	if chunk != line1 {
		t.Errorf("chunk len = %d, want %d", len(chunk), len(line1))
	}
	if rest != line2 {
		t.Errorf("rest = %q, want %q", rest, line2)
	}
}

func TestTakeBodyChunk_EmptyBody(t *testing.T) {
	s := NewShaper(&fakeCompleter{}, Config{Model: "m"})
	chunk, rest := s.takeBodyChunk(MusicInput{Post: &redditJSON.RedditPost{}}, "")
	if chunk != "" || rest != "" {
		t.Errorf("empty body should give empty chunk and rest; got %q / %q", chunk, rest)
	}
}

func TestContextLimit_DefaultWhenZero(t *testing.T) {
	s := NewShaper(&fakeCompleter{}, Config{Model: "m"})
	if got := s.contextLimit(); got != DefaultContextLimit {
		t.Errorf("contextLimit() = %d, want %d", got, DefaultContextLimit)
	}
}

func TestContextLimit_UsesConfigValue(t *testing.T) {
	s := NewShaper(&fakeCompleter{}, Config{Model: "m", ContextLimit: 4096})
	if got := s.contextLimit(); got != 4096 {
		t.Errorf("contextLimit() = %d, want 4096", got)
	}
}
