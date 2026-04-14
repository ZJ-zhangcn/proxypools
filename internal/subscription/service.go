package subscription

import (
	"context"

	"proxypools/internal/model"
	"proxypools/internal/parser"
)

type Parser interface {
	ParseSubscription(input []byte) ([]model.Node, error)
}

type SubscriptionRepository interface {
	GetPrimarySubscription(ctx context.Context) (*model.Subscription, error)
}

type Service struct {
	fetcher *Fetcher
}

func NewService(fetcher *Fetcher) *Service {
	if fetcher == nil {
		fetcher = NewFetcher()
	}
	return &Service{fetcher: fetcher}
}

func (s *Service) Refresh(ctx context.Context, subscriptionURL string) ([]model.Node, error) {
	content, err := s.fetcher.Fetch(ctx, subscriptionURL)
	if err != nil {
		return nil, err
	}
	return parser.ParseSubscription(content)
}
