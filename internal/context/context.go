package context

import (
	"context"
	"os"
	"strings"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

const EnvLogLevel = "LOG_LEVEL"

type Ctx interface {
	context.Context
	Log() log.Logger
}

type RedditSpyCtx struct {
	context.Context
	log log.Logger
}

func (c RedditSpyCtx) Log() log.Logger {
	return c.log
}

func New(ctx context.Context) RedditSpyCtx {
	var logger log.Logger
	logger = log.NewLogfmtLogger(os.Stderr)
	logger = level.NewFilter(logger, levelOptionFromEnv())
	logger = log.With(logger, "ts", log.DefaultTimestampUTC)

	return RedditSpyCtx{
		Context: ctx,
		log:     logger,
	}
}

// levelOptionFromEnv reads LOG_LEVEL and maps it to a go-kit level filter.
// Unknown or empty values fall through to "info" — production default.
func levelOptionFromEnv() level.Option {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvLogLevel))) {
	case "debug":
		return level.AllowDebug()
	case "warn", "warning":
		return level.AllowWarn()
	case "error":
		return level.AllowError()
	case "none", "off":
		return level.AllowNone()
	default:
		return level.AllowInfo()
	}
}
