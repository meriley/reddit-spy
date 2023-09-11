package context

import (
	"github.com/go-kit/kit/log"
	"github.com/go-kit/log/level"
	"golang.org/x/net/context"
	"os"
)

type Context interface {
	Context() context.Context
	Log() log.Logger
	Close()
}

type Ctx struct {
	context context.Context
	log     log.Logger
	Done    chan struct{}
}

func (c *Ctx) Context() context.Context {
	return c.context
}
func (c *Ctx) Log() log.Logger {
	return c.log
}

func NewContext() Ctx {
	var logger log.Logger
	logger = log.NewLogfmtLogger(os.Stderr)
	logger = level.NewFilter(logger, level.AllowAll())
	logger = log.With(logger, "ts", log.DefaultTimestampUTC)

	return Ctx{
		context: context.Background(),
		log:     logger,
		Done:    make(chan struct{}),
	}
}
