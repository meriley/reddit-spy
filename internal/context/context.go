package context

import (
	"os"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/log/level"
	"golang.org/x/net/context"
)

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
	logger = level.NewFilter(logger, level.AllowAll())
	logger = log.With(logger, "ts", log.DefaultTimestampUTC)

	return RedditSpyCtx{
		Context: ctx,
		log:     logger,
	}
}
