package agent

import (
	"context"
	"fmt"

	"github.com/whosthatknocking/abx/pkg/types"
)

type FallbackProvider struct {
	primary  Provider
	fallback Provider
}

func NewFallback(primary, fallback Provider) Provider {
	if fallback == nil {
		return primary
	}
	return &FallbackProvider{
		primary:  primary,
		fallback: fallback,
	}
}

func (p *FallbackProvider) Chat(ctx context.Context, messages []types.Message, tools []types.Tool) (types.AgentResponse, error) {
	if p.primary == nil {
		if p.fallback == nil {
			return types.AgentResponse{}, fmt.Errorf("no agent provider configured")
		}
		return p.fallback.Chat(ctx, messages, tools)
	}

	response, err := p.primary.Chat(ctx, messages, tools)
	if err == nil {
		return response, nil
	}
	if p.fallback == nil {
		return types.AgentResponse{}, err
	}

	fallbackResponse, fallbackErr := p.fallback.Chat(ctx, messages, tools)
	if fallbackErr == nil {
		return fallbackResponse, nil
	}
	return types.AgentResponse{}, fmt.Errorf("primary agent failed: %v; fallback agent failed: %w", err, fallbackErr)
}

func (p *FallbackProvider) Check(ctx context.Context) error {
	if p.primary != nil {
		if err := p.primary.Check(ctx); err == nil {
			return nil
		} else if p.fallback == nil {
			return err
		}
	}
	if p.fallback == nil {
		return fmt.Errorf("no agent provider configured")
	}
	return p.fallback.Check(ctx)
}
