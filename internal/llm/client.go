// Package llm wraps an OpenAI-compatible chat-completions endpoint (in
// production: the in-cluster vLLM service) with the narrow contract reddit-spy
// needs to shape Reddit matches into Discord-ready narratives.
package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/sashabaranov/go-openai"
)

const (
	EnvBaseURL      = "LLM_BASE_URL"
	EnvModel        = "LLM_MODEL"
	EnvAPIKey       = "LLM_API_KEY"
	EnvTimeout      = "LLM_TIMEOUT"
	EnvTone         = "LLM_TONE"
	EnvContextLimit = "LLM_CONTEXT_LIMIT"

	DefaultTimeout      = 30 * time.Second
	DefaultContextLimit = 8192
	SummaryCharBudget   = 3800
)

// Config captures everything the shaper needs to talk to the model.
type Config struct {
	BaseURL      string
	Model        string
	APIKey       string
	Timeout      time.Duration
	Tone         string
	ContextLimit int
}

// ConfigFromEnv reads Config from the process environment. Returns an error
// if the required BaseURL or Model are missing or LLM_TIMEOUT is malformed.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		BaseURL:      os.Getenv(EnvBaseURL),
		Model:        os.Getenv(EnvModel),
		APIKey:       os.Getenv(EnvAPIKey),
		Tone:         os.Getenv(EnvTone),
		Timeout:      DefaultTimeout,
		ContextLimit: DefaultContextLimit,
	}
	if cfg.BaseURL == "" {
		return cfg, fmt.Errorf("missing %s", EnvBaseURL)
	}
	if cfg.Model == "" {
		return cfg, fmt.Errorf("missing %s", EnvModel)
	}
	if raw := os.Getenv(EnvTimeout); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, fmt.Errorf("invalid %s=%q: %w", EnvTimeout, raw, err)
		}
		cfg.Timeout = d
	}
	if raw := os.Getenv(EnvContextLimit); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return cfg, fmt.Errorf("invalid %s=%q: must be a positive integer", EnvContextLimit, raw)
		}
		cfg.ContextLimit = n
	}
	if cfg.APIKey == "" {
		// vLLM accepts any token by default; the SDK refuses an empty one.
		cfg.APIKey = "EMPTY"
	}
	return cfg, nil
}

// ChatCompleter is the slice of the OpenAI SDK surface the shaper exercises.
// *openai.Client satisfies it natively; tests supply their own fakes.
type ChatCompleter interface {
	CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

// NewClient builds an *openai.Client pre-configured for the supplied Config.
// The HTTP client inherits cfg.Timeout so stuck connections don't block the
// Reddit poll loop indefinitely.
func NewClient(cfg Config) (*openai.Client, error) {
	if cfg.BaseURL == "" || cfg.Model == "" {
		return nil, errors.New("llm.NewClient: BaseURL and Model are required")
	}
	openaiCfg := openai.DefaultConfig(cfg.APIKey)
	openaiCfg.BaseURL = cfg.BaseURL
	openaiCfg.HTTPClient = &http.Client{Timeout: cfg.Timeout}
	return openai.NewClientWithConfig(openaiCfg), nil
}
