package repository

import (
	"context"

	"github.com/whosthatknocking/abx/pkg/types"
)

type Repository interface {
	SaveMessage(ctx context.Context, conversationID, sessionID string, msg types.Message) error
	GetActiveSessionID(ctx context.Context, conversationID string) (string, error)
	GetHistory(ctx context.Context, conversationID, sessionID string, limit int) ([]types.Message, error)
	GetActiveHistory(ctx context.Context, conversationID string, limit int) ([]types.Message, error)
	SaveConversationSummary(ctx context.Context, conversationID, sessionID, summary string) error
	GetConversationSummary(ctx context.Context, conversationID, sessionID string) (string, error)
	GetActiveConversationSummary(ctx context.Context, conversationID string) (string, error)
	SaveSessionPersona(ctx context.Context, conversationID, sessionID, persona string) error
	GetSessionPersona(ctx context.Context, conversationID, sessionID string) (string, error)
	GetActiveSessionPersona(ctx context.Context, conversationID string) (string, error)
	SaveSessionFormat(ctx context.Context, conversationID, sessionID, format string) error
	GetSessionFormat(ctx context.Context, conversationID, sessionID string) (string, error)
	GetActiveSessionFormat(ctx context.Context, conversationID string) (string, error)
	RotateConversationSession(ctx context.Context, conversationID string) (string, error)
	SavePendingApproval(ctx context.Context, conversationID, sessionID string, approval types.PendingApproval) error
	GetPendingApproval(ctx context.Context, conversationID, requestID string) (*types.PendingApproval, error)
	GetActivePendingApproval(ctx context.Context, conversationID string) (*types.PendingApproval, error)
	ClearPendingApproval(ctx context.Context, conversationID, requestID string) error
}
