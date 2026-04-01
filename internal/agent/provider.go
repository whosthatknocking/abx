package agent

import (
	"context"

	"github.com/whosthatknocking/abx/pkg/types"
)

type Provider interface {
	Chat(ctx context.Context, messages []types.Message, tools []types.Tool) (types.AgentResponse, error)
	Check(ctx context.Context) error
}

type Switcher interface {
	SwapPrimaryAndFallback() error
}
