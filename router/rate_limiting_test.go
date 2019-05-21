package router

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/topfreegames/pitaya/cluster"
	"github.com/topfreegames/pitaya/config"
	"github.com/topfreegames/pitaya/constants"
	"github.com/topfreegames/pitaya/errors"
	"github.com/topfreegames/pitaya/route"
	"github.com/topfreegames/pitaya/session"
)

func TestRateLimitingTake(t *testing.T) {
	t.Parallel()

	var (
		limit    = 3
		interval = time.Second
		now      = time.Now()
		r        *RateLimiting
	)

	tables := map[string]struct {
		before func()
		err    error
	}{
		"test_can_take_on_first_time": {
			before: func() {},
			err:    nil,
		},
		"test_can_take_missing_one_to_limit": {
			before: func() {
				r.Take(now)
				r.Take(now)
			},
			err: nil,
		},
		"test_can_take_when_oldest_request_expired": {
			before: func() {
				r.Take(now.Add(-2 * interval))
				r.Take(now)
				r.Take(now)
			},
			err: nil,
		},
		"test_cant_take_when_exceeded_limit": {
			before: func() {
				r.Take(now)
				r.Take(now)
				r.Take(now)
			},
			err: errors.NewError(
				constants.ErrExceededRateLimiting,
				errors.ErrTooManyRequests),
		},
		"test_concurrent_take": {
			before: func() {
				var errorsCount int32
				var wg sync.WaitGroup

				for i := 0; i < 100; i++ {
					wg.Add(1)
					go func() {
						if err := r.Take(now); err != nil {
							atomic.AddInt32(&errorsCount, 1)
						}

						wg.Done()
					}()
				}

				wg.Wait()
				assert.Equal(t, int32(100-limit), errorsCount)
			},
			err: errors.NewError(
				constants.ErrExceededRateLimiting,
				errors.ErrTooManyRequests),
		},
	}

	for name, table := range tables {
		t.Run(name, func(t *testing.T) {
			r = NewRateLimiting(limit, interval)

			table.before()
			err := r.Take(now)
			assert.Equal(t, table.err, err)
		})
	}
}

func TestWithRateLimiting(t *testing.T) {
	t.Parallel()

	var (
		limit      = 3
		interval   = 100 * time.Millisecond
		ctx        = context.Background()
		newSession = func() *session.Session { return session.New(nil, false) }
		r          = route.NewRoute("server", "service", "method")
		f          = func(
			_ context.Context,
			_ *route.Route,
			_ []byte,
			_ map[string]*cluster.Server,
		) (*cluster.Server, error) {
			return nil, nil
		}
	)

	getConfig := func(forceDisable bool) *config.Config {
		c := viper.New()
		c.Set("pitaya.router.ratelimiting.limit", limit)
		c.Set("pitaya.router.ratelimiting.interval", interval)
		c.Set("pitaya.router.ratelimiting.forceDisable", forceDisable)
		return config.NewConfig(c)
	}

	tables := map[string]struct {
		ctx    func() context.Context
		config *config.Config
		err    error
	}{
		"test_ctx_without_session": {
			ctx:    func() context.Context { return ctx },
			config: getConfig(false),
			err:    nil,
		},
		"test_save_rate_limiting_on_session": {
			ctx: func() context.Context {
				return context.WithValue(ctx, constants.SessionCtxKey, newSession())
			},
			config: getConfig(false),
			err:    nil,
		},
		"test_exceed_rate_limiting": {
			ctx: func() context.Context {
				ctx := context.WithValue(ctx, constants.SessionCtxKey, newSession())
				for i := 0; i < limit; i++ {
					_, err := WithRateLimiting(getConfig(false), f)(ctx, r, nil, nil)
					assert.NoError(t, err)
				}
				return ctx
			},
			config: getConfig(false),
			err: errors.NewError(
				constants.ErrExceededRateLimiting,
				errors.ErrTooManyRequests),
		},
		"test_free_request_after_interval": {
			ctx: func() context.Context {
				ctx := context.WithValue(ctx, constants.SessionCtxKey, newSession())
				for i := 0; i < limit; i++ {
					_, err := WithRateLimiting(getConfig(false), f)(ctx, r, nil, nil)
					assert.NoError(t, err)
				}
				time.Sleep(interval)
				return ctx
			},
			config: getConfig(false),
			err:    nil,
		},
		"test_force_disable": {
			ctx: func() context.Context {
				ctx := context.WithValue(ctx, constants.SessionCtxKey, newSession())
				for i := 0; i < limit; i++ {
					_, err := WithRateLimiting(getConfig(true), f)(ctx, r, nil, nil)
					assert.NoError(t, err)
				}
				return ctx
			},
			config: getConfig(true),
			err:    nil,
		},
	}

	for name, table := range tables {
		t.Run(name, func(t *testing.T) {
			_, err := WithRateLimiting(table.config, f)(table.ctx(), r, nil, nil)
			assert.Equal(t, table.err, err)
		})
	}
}
