package reddit

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	redditAndroidClientID = "ohXpoqrZYub1kg"
	spoofAuthEndpoint     = "https://www.reddit.com/auth/v2/oauth/access-token/loid"
	genericAuthEndpoint   = "https://www.reddit.com/api/v1/access_token"
	genericClientID       = "3XfBJWliHvqACnXrfIYlLw"
	spoofAPIBaseURL       = "https://oauth.reddit.com"

	defaultSpoofUserAgent = "Reddit/2026.06.0/Android 15"

	rateLimitThreshold    = 10
	rateLimitSafetyMargin = 1.25

	baseBackoffDelay      = 60 * time.Second
	maxBackoffDelay       = 10 * time.Minute
	forbiddenBackoffDelay = 1 * time.Hour
	maxRetries            = 5
)

var (
	ErrForbidden = errors.New("reddit: access forbidden (rate limit ban)")
	ErrNotFound  = errors.New("reddit: not found")
)

// RateLimitNotifier is called when the client starts a rate limit backoff.
type RateLimitNotifier func(until time.Time)

// SpoofConfig holds configuration for the spoof client.
type SpoofConfig struct {
	UserAgent             string
	SessionRotateRequests int
	JitterPercent         int
}

// Post mirrors the Reddit post data returned by the listing API.
type Post struct {
	Author      string  `json:"author"`
	ID          string  `json:"id"`
	Permalink   string  `json:"permalink"`
	Selftext    string  `json:"selftext"`
	Subreddit   string  `json:"subreddit"`
	Thumbnail   string  `json:"thumbnail"`
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	Score       int     `json:"score"`
	NumComments int     `json:"num_comments"`
	CreatedUTC  float64 `json:"created_utc"`
}

type listingChild struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

type listingData struct {
	After    string         `json:"after"`
	Children []listingChild `json:"children"`
}

type listingResponse struct {
	Data listingData `json:"data"`
}

// SpoofClient is a Reddit client that spoofs the official Android app.
// It requires no registered app credentials.
type SpoofClient struct {
	cfg        SpoofConfig
	httpClient *http.Client
	userAgent  string

	accessToken string
	tokenExpiry time.Time
	deviceID    string
	loid        string
	session     string

	mu           sync.RWMutex
	rateLimiter  *RateLimiter
	notifier     RateLimitNotifier
	backoffStore BackoffStore

	rateLimitRemaining int
	rateLimitReset     int
	rateLimitUsed      int

	requestCount int

	backoffUntil  time.Time
	backoffRetry  int
	backoffLoaded bool
	wasInBackoff  bool
}

// NewSpoofClient creates a new mobile spoof Reddit client.
func NewSpoofClient(cfg SpoofConfig) *SpoofClient {
	ua := cfg.UserAgent
	if ua == "" {
		ua = defaultSpoofUserAgent
	}
	return &SpoofClient{
		cfg:       cfg,
		userAgent: ua,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		deviceID:           uuid.New().String(),
		rateLimiter:        NewRateLimiter(0),
		backoffStore:       NopBackoffStore{},
		rateLimitRemaining: 100,
	}
}

// SetRateLimitNotifier sets a callback invoked when rate limiting begins.
func (c *SpoofClient) SetRateLimitNotifier(fn RateLimitNotifier) {
	c.notifier = fn
}

func (c *SpoofClient) authenticate(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.accessToken != "" && time.Now().Before(c.tokenExpiry) && c.rateLimitRemaining > rateLimitThreshold {
		return nil
	}

	if err := c.authenticateMobileSpoof(ctx); err != nil {
		slog.Warn("mobile spoof auth failed, trying generic auth", "error", err)
		if err := c.authenticateGeneric(ctx); err != nil {
			return fmt.Errorf("all auth methods failed: %w", err)
		}
	}
	return nil
}

func (c *SpoofClient) authenticateMobileSpoof(ctx context.Context) error {
	body := map[string][]string{
		"scopes": {"*", "email", "pii"},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal auth body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spoofAuthEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create auth request: %w", err)
	}

	basicAuth := base64.StdEncoding.EncodeToString([]byte(redditAndroidClientID + ":"))
	req.Header.Set("Authorization", "Basic "+basicAuth)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("X-Reddit-Device-Id", c.deviceID)
	req.Header.Set("client-vendor-id", c.deviceID)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("x-reddit-retry", "algo=no-retries")
	req.Header.Set("x-reddit-compression", "1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("auth request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read auth response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mobile spoof auth failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var authResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &authResp); err != nil {
		return fmt.Errorf("decode auth response: %w", err)
	}
	if authResp.AccessToken == "" {
		return fmt.Errorf("no access token in response: %s", string(respBody))
	}

	c.accessToken = authResp.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(authResp.ExpiresIn-60) * time.Second)
	c.rateLimitRemaining = 100

	if loid := resp.Header.Get("x-reddit-loid"); loid != "" {
		c.loid = loid
	}
	if session := resp.Header.Get("x-reddit-session"); session != "" {
		c.session = session
	}

	slog.Info("mobile spoof auth successful",
		"expires_in", authResp.ExpiresIn,
		"device_id", c.deviceID[:8]+"...",
	)
	return nil
}

func (c *SpoofClient) authenticateGeneric(ctx context.Context) error {
	data := url.Values{}
	data.Set("grant_type", "https://oauth.reddit.com/grants/installed_client")
	data.Set("device_id", generateDeviceID())

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, genericAuthEndpoint, bytes.NewReader([]byte(data.Encode())))
	if err != nil {
		return fmt.Errorf("create generic auth request: %w", err)
	}

	basicAuth := base64.StdEncoding.EncodeToString([]byte(genericClientID + ":"))
	req.Header.Set("Authorization", "Basic "+basicAuth)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("generic auth request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read generic auth response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("generic auth failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var authResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &authResp); err != nil {
		return fmt.Errorf("decode generic auth response: %w", err)
	}
	if authResp.AccessToken == "" {
		return fmt.Errorf("no access token in generic response: %s", string(respBody))
	}

	c.accessToken = authResp.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(authResp.ExpiresIn-60) * time.Second)
	c.rateLimitRemaining = 100

	slog.Info("generic auth successful", "expires_in", authResp.ExpiresIn)
	return nil
}

func generateDeviceID() string {
	id := uuid.New().String()
	clean := ""
	for _, ch := range id {
		if ch != '-' {
			clean += string(ch)
		}
		if len(clean) >= 20 {
			break
		}
	}
	return clean
}

func cryptoRandFloat64() float64 {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0
	}
	n := binary.BigEndian.Uint64(buf[:]) >> 11
	return float64(n) / float64(1<<53)
}

func (c *SpoofClient) rotateSession() {
	oldID := c.deviceID
	c.deviceID = uuid.New().String()
	c.accessToken = ""
	c.tokenExpiry = time.Time{}
	c.loid = ""
	c.session = ""
	c.requestCount = 0

	slog.Info("rotated spoof session",
		"old_device_id", oldID[:8]+"...",
		"new_device_id", c.deviceID[:8]+"...",
	)
}

func (c *SpoofClient) rateLimit() {
	c.mu.RLock()
	remaining := c.rateLimitRemaining
	reset := c.rateLimitReset
	c.mu.RUnlock()

	var delay time.Duration
	switch {
	case remaining > 0 && reset > 0:
		resetDuration := time.Duration(reset) * time.Second
		delay = time.Duration(float64(resetDuration) / float64(remaining) * rateLimitSafetyMargin)
		delay = max(delay, 100*time.Millisecond)
	case remaining <= 0 && reset > 0:
		delay = time.Duration(reset) * time.Second
		slog.Info("rate limit exhausted, waiting for reset", "reset_seconds", reset)
	default:
		delay = 600 * time.Millisecond
	}

	jitterPct := 30
	if c.cfg.JitterPercent > 0 {
		jitterPct = c.cfg.JitterPercent
	}
	jitter := time.Duration(float64(delay) * (float64(jitterPct) / 100.0) * cryptoRandFloat64())
	delay += jitter

	c.rateLimiter.WaitWithDynamic(delay)
}

func (c *SpoofClient) updateRateLimits(resp *http.Response) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if v := resp.Header.Get("x-ratelimit-remaining"); v != "" {
		if val, err := strconv.ParseFloat(v, 64); err == nil {
			c.rateLimitRemaining = int(val)
		}
	}
	if v := resp.Header.Get("x-ratelimit-reset"); v != "" {
		if val, err := strconv.Atoi(v); err == nil {
			c.rateLimitReset = val
		}
	}
	if v := resp.Header.Get("x-ratelimit-used"); v != "" {
		if val, err := strconv.Atoi(v); err == nil {
			c.rateLimitUsed = val
		}
	}

	if c.rateLimitRemaining > 0 && c.rateLimitRemaining <= rateLimitThreshold*2 {
		slog.Debug("rate limit status",
			"remaining", c.rateLimitRemaining,
			"reset_in", c.rateLimitReset,
			"used", c.rateLimitUsed,
		)
	}
}

func (c *SpoofClient) loadBackoffStateOnce(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.backoffLoaded {
		return
	}
	c.backoffLoaded = true

	state, err := c.backoffStore.Load(ctx)
	if err != nil {
		slog.Warn("failed to load backoff state", "error", err)
		return
	}
	if state != nil {
		c.backoffUntil = state.Until
		c.backoffRetry = state.RetryCount
		c.wasInBackoff = true
	}
}

func (c *SpoofClient) doRequest(ctx context.Context, endpoint string, params url.Values) ([]byte, error) {
	c.loadBackoffStateOnce(ctx)

	c.mu.RLock()
	backoffUntil := c.backoffUntil
	backoffRetry := c.backoffRetry
	c.mu.RUnlock()

	if time.Now().Before(backoffUntil) {
		remaining := time.Until(backoffUntil)
		slog.Warn("resuming backoff from previous session",
			"remaining_seconds", remaining.Seconds(),
			"retry", backoffRetry,
		)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(remaining):
		}
	}

	return c.doRequestWithRetry(ctx, endpoint, params, backoffRetry)
}

func (c *SpoofClient) doRequestWithRetry(ctx context.Context, endpoint string, params url.Values, retry int) ([]byte, error) {
	if err := c.authenticate(ctx); err != nil {
		return nil, err
	}

	rotateAfter := c.cfg.SessionRotateRequests
	if rotateAfter <= 0 {
		rotateAfter = 200
	}
	c.mu.Lock()
	c.requestCount++
	needsRotation := c.requestCount >= rotateAfter
	c.mu.Unlock()

	if needsRotation {
		c.mu.Lock()
		c.rotateSession()
		c.mu.Unlock()
		if err := c.authenticate(ctx); err != nil {
			return nil, err
		}
	}

	c.rateLimit()

	reqURL := spoofAPIBaseURL + endpoint
	if params != nil {
		reqURL += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	c.mu.RLock()
	token := c.accessToken
	loid := c.loid
	session := c.session
	c.mu.RUnlock()

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("X-Reddit-Device-Id", c.deviceID)
	if loid != "" {
		req.Header.Set("x-reddit-loid", loid)
	}
	if session != "" {
		req.Header.Set("x-reddit-session", session)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	c.updateRateLimits(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		if retry >= maxRetries {
			return nil, fmt.Errorf("rate limited by reddit: max retries (%d) exceeded", maxRetries)
		}

		backoff := min(baseBackoffDelay*time.Duration(1<<retry), maxBackoffDelay)
		until := time.Now().Add(backoff)

		c.mu.Lock()
		c.backoffUntil = until
		c.backoffRetry = retry + 1
		c.wasInBackoff = true
		c.mu.Unlock()

		_ = c.backoffStore.Save(ctx, &BackoffState{Until: until, RetryCount: retry + 1})

		if c.notifier != nil {
			c.notifier(until)
		}

		slog.Warn("rate limited by reddit, backing off",
			"retry", retry+1,
			"max_retries", maxRetries,
			"backoff_seconds", backoff.Seconds(),
		)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}

		return c.doRequestWithRetry(ctx, endpoint, params, retry+1)
	}

	if resp.StatusCode == http.StatusForbidden {
		bodyPreview := string(body)
		if len(bodyPreview) > 200 {
			bodyPreview = bodyPreview[:200] + "..."
		}

		if !isRateLimitBan(body) {
			slog.Warn("reddit returned 403 for suspended/banned resource",
				"endpoint", endpoint,
				"body", bodyPreview,
			)
			return nil, fmt.Errorf("%w: resource may be private or banned", ErrNotFound)
		}

		until := time.Now().Add(forbiddenBackoffDelay)

		c.mu.Lock()
		c.backoffUntil = until
		c.backoffRetry = maxRetries
		c.wasInBackoff = true
		c.mu.Unlock()

		_ = c.backoffStore.Save(ctx, &BackoffState{Until: until, RetryCount: maxRetries})

		if c.notifier != nil {
			c.notifier(until)
		}

		slog.Error("reddit returned 403 forbidden - rate limit ban",
			"backoff_minutes", forbiddenBackoffDelay.Minutes(),
			"endpoint", endpoint,
			"body", bodyPreview,
		)

		return nil, fmt.Errorf("%w: %s", ErrForbidden, string(body))
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: subreddit may not exist", ErrNotFound)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		c.mu.Lock()
		c.accessToken = ""
		c.tokenExpiry = time.Time{}
		c.mu.Unlock()

		if retry < 1 {
			slog.Warn("auth token rejected, re-authenticating")
			return c.doRequestWithRetry(ctx, endpoint, params, retry+1)
		}
		return nil, fmt.Errorf("authentication failed after retry: status %d", resp.StatusCode)
	}

	if resp.StatusCode >= 500 {
		if retry >= maxRetries {
			return nil, fmt.Errorf("server error: max retries exceeded, last status %d", resp.StatusCode)
		}

		backoff := min(time.Duration(2<<retry)*time.Second, 30*time.Second)
		slog.Warn("reddit server error, retrying",
			"status", resp.StatusCode,
			"retry", retry+1,
			"backoff_seconds", backoff.Seconds(),
		)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}

		return c.doRequestWithRetry(ctx, endpoint, params, retry+1)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	c.mu.Lock()
	wasInBackoff := c.wasInBackoff
	c.backoffUntil = time.Time{}
	c.backoffRetry = 0
	c.wasInBackoff = false
	c.mu.Unlock()

	if wasInBackoff {
		if err := c.backoffStore.Clear(ctx); err != nil {
			slog.Warn("failed to clear backoff state", "error", err)
		}
	}

	return body, nil
}

// isRateLimitBan returns true when a 403 body indicates an IP-level ban
// rather than a per-resource block.
func isRateLimitBan(body []byte) bool {
	if bytes.Contains(body, []byte("blocked by network security")) ||
		bytes.Contains(body, []byte("cdn-cgi")) ||
		bytes.Contains(body, []byte("Cloudflare")) {
		return true
	}
	if len(body) < 200 {
		var resp struct {
			Message string `json:"message"`
			Error   int    `json:"error"`
		}
		if json.Unmarshal(body, &resp) == nil && resp.Error == 403 {
			return false
		}
	}
	return len(body) > 200
}

// GetSubredditPosts fetches the newest posts from a subreddit via the OAuth API.
// Returns posts, an "after" cursor for pagination, and any error.
func (c *SpoofClient) GetSubredditPosts(ctx context.Context, subreddit, after string, limit int) ([]*Post, string, error) {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	if after != "" {
		params.Set("after", after)
	}

	body, err := c.doRequest(ctx, "/r/"+subreddit+"/new", params)
	if err != nil {
		return nil, "", err
	}

	var listing listingResponse
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, "", fmt.Errorf("decode subreddit listing: %w", err)
	}

	posts := make([]*Post, 0, len(listing.Data.Children))
	for _, child := range listing.Data.Children {
		if child.Kind != "t3" {
			continue
		}
		var p Post
		if err := json.Unmarshal(child.Data, &p); err != nil {
			slog.Warn("failed to decode post", "error", err)
			continue
		}
		posts = append(posts, &p)
	}

	return posts, listing.Data.After, nil
}
