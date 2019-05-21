package router

import (
	"container/list"
	"context"
	"sync"
	"time"

	"github.com/topfreegames/pitaya/cluster"
	"github.com/topfreegames/pitaya/config"
	"github.com/topfreegames/pitaya/constants"
	"github.com/topfreegames/pitaya/errors"
	"github.com/topfreegames/pitaya/logger"
	"github.com/topfreegames/pitaya/route"
	"github.com/topfreegames/pitaya/session"
)

// WithRateLimiting wraps routingFunc and controls
// player rate through session.
func WithRateLimiting(c *config.Config, f RoutingFunc) RoutingFunc {
	limit := c.GetInt("pitaya.router.ratelimiting.limit")
	interval := c.GetDuration("pitaya.router.ratelimiting.interval")
	forceDisable := c.GetBool("pitaya.router.ratelimiting.forcedisable")
	if forceDisable {
		logger.Log.Warn("rate limiting is forcibly disabled")
	}

	return func(
		ctx context.Context,
		r *route.Route,
		payload []byte,
		servers map[string]*cluster.Server,
	) (*cluster.Server, error) {
		if forceDisable {
			return f(ctx, r, payload, servers)
		}

		sessionVal := ctx.Value(constants.SessionCtxKey)
		if sessionVal == nil {
			logger.Log.Warnf("session not found, cant rate limit, route: %s", r.String())
			return f(ctx, r, payload, servers)
		}

		var (
			rl  *RateLimiting
			s   = sessionVal.(*session.Session)
			now = time.Now()
		)

		if s.HasKey(constants.RateLimitingKey) {
			rl = s.Get(constants.RateLimitingKey).(*RateLimiting)
		} else {
			rl = NewRateLimiting(limit, interval)
			s.Set(constants.RateLimitingKey, rl)
		}

		err := rl.Take(now)
		if err != nil {
			logger.Log.Errorf("Route=%s, UID=%s, ID=%d, Error=%s",
				r.String(), s.UID(), s.ID(), err.Error())
			return nil, err
		}

		return f(ctx, r, payload, servers)
	}
}

// RateLimiting uses the leaky bucket algorithm to
// control player throughput over their session.
type RateLimiting struct {
	limit    int
	interval time.Duration
	mutex    sync.Mutex
	times    list.List
}

// NewRateLimiting returns an initialized *RateLimiting
func NewRateLimiting(limit int, interval time.Duration) *RateLimiting {
	r := &RateLimiting{
		limit:    limit,
		interval: interval,
	}

	r.times.Init()

	return r
}

// Take saves the now as time taken or returns an error if
// in the limit of rate limiting
func (r *RateLimiting) Take(now time.Time) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if r.times.Len() < r.limit {
		r.times.PushBack(now)
		return nil
	}

	front := r.times.Front()
	if diff := now.Sub(front.Value.(time.Time)); diff < r.interval {
		err := constants.ErrExceededRateLimiting
		return errors.NewError(err, errors.ErrTooManyRequests)
	}

	front.Value = now
	r.times.MoveToBack(front)
	return nil
}
