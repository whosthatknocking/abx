package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/whosthatknocking/abx/pkg/types"
)

type FallbackProvider struct {
	mu       sync.RWMutex
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
	p.mu.RLock()
	primary := p.primary
	fallback := p.fallback
	p.mu.RUnlock()

	if primary == nil {
		if fallback == nil {
			return types.AgentResponse{}, fmt.Errorf("no agent provider configured")
		}
		return fallback.Chat(ctx, messages, tools)
	}

	response, err := primary.Chat(ctx, messages, tools)
	if err == nil {
		return response, nil
	}
	if fallback == nil {
		return types.AgentResponse{}, err
	}

	fallbackResponse, fallbackErr := fallback.Chat(ctx, messages, tools)
	if fallbackErr == nil {
		return fallbackResponse, nil
	}
	return types.AgentResponse{}, fmt.Errorf("primary agent failed: %v; fallback agent failed: %w", err, fallbackErr)
}

func (p *FallbackProvider) Check(ctx context.Context) error {
	p.mu.RLock()
	primary := p.primary
	fallback := p.fallback
	p.mu.RUnlock()

	if primary != nil {
		if err := primary.Check(ctx); err == nil {
			return nil
		} else if fallback == nil {
			return err
		}
	}
	if fallback == nil {
		return fmt.Errorf("no agent provider configured")
	}
	return fallback.Check(ctx)
}

func (p *FallbackProvider) ChatPrimary(ctx context.Context, messages []types.Message, tools []types.Tool) (types.AgentResponse, error) {
	p.mu.RLock()
	primary := p.primary
	p.mu.RUnlock()

	if primary == nil {
		return types.AgentResponse{}, fmt.Errorf("no primary agent provider configured")
	}
	return primary.Chat(ctx, messages, tools)
}

func (p *FallbackProvider) CheckPrimary(ctx context.Context) error {
	p.mu.RLock()
	primary := p.primary
	p.mu.RUnlock()

	if primary == nil {
		return fmt.Errorf("no primary agent provider configured")
	}
	return primary.Check(ctx)
}

func (p *FallbackProvider) SwapPrimaryAndFallback() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fallback == nil {
		return fmt.Errorf("fallback agent is not configured")
	}
	p.primary, p.fallback = p.fallback, p.primary
	return nil
}
