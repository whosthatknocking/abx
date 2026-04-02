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
	return p.ChatWithOptions(ctx, messages, tools, types.AgentOptions{})
}

func (p *FallbackProvider) ChatWithOptions(ctx context.Context, messages []types.Message, tools []types.Tool, options types.AgentOptions) (types.AgentResponse, error) {
	p.mu.RLock()
	primary := p.primary
	fallback := p.fallback
	p.mu.RUnlock()

	if primary == nil {
		if fallback == nil {
			return types.AgentResponse{}, fmt.Errorf("no agent provider configured")
		}
		return chatWithOptions(ctx, fallback, messages, tools, options)
	}

	response, err := chatWithOptions(ctx, primary, messages, tools, options)
	if err == nil {
		return response, nil
	}
	if fallback == nil {
		return types.AgentResponse{}, err
	}

	fallbackResponse, fallbackErr := chatWithOptions(ctx, fallback, messages, tools, options)
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
	return p.ChatPrimaryWithOptions(ctx, messages, tools, types.AgentOptions{})
}

func (p *FallbackProvider) ChatPrimaryWithOptions(ctx context.Context, messages []types.Message, tools []types.Tool, options types.AgentOptions) (types.AgentResponse, error) {
	p.mu.RLock()
	primary := p.primary
	p.mu.RUnlock()

	if primary == nil {
		return types.AgentResponse{}, fmt.Errorf("no primary agent provider configured")
	}
	return chatWithOptions(ctx, primary, messages, tools, options)
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

func chatWithOptions(ctx context.Context, provider Provider, messages []types.Message, tools []types.Tool, options types.AgentOptions) (types.AgentResponse, error) {
	if configurable, ok := provider.(OptionsProvider); ok {
		return configurable.ChatWithOptions(ctx, messages, tools, options)
	}
	return provider.Chat(ctx, messages, tools)
}
