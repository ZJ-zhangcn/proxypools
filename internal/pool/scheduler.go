package pool

import (
	"context"
	"time"
)

type Scheduler struct {
	SubscriptionEvery time.Duration
	HealthEvery       time.Duration
}

func (s Scheduler) Start(ctx context.Context, refresh func(context.Context), check func(context.Context)) {
	refreshTicker := time.NewTicker(s.SubscriptionEvery)
	healthTicker := time.NewTicker(s.HealthEvery)

	go func() {
		defer refreshTicker.Stop()
		defer healthTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-refreshTicker.C:
				refresh(ctx)
			case <-healthTicker.C:
				check(ctx)
			}
		}
	}()
}
