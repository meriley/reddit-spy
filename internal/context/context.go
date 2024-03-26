package context

import (
	"os"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/log/level"
	"golang.org/x/net/context"
)

type Context interface {
	context.Context
	Log() log.Logger
}

type Ctx struct {
	context.Context
	log log.Logger
}

func (c Ctx) Log() log.Logger {
	return c.log
}

func New(ctx context.Context) Ctx {
	var logger log.Logger
	logger = log.NewLogfmtLogger(os.Stderr)
	logger = level.NewFilter(logger, level.AllowAll())
	logger = log.With(logger, "ts", log.DefaultTimestampUTC)

	return Ctx{
		Context: ctx,
		log:     logger,
	}
}
