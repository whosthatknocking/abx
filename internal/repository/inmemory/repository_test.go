package inmemory

import (
	"context"
	"testing"
	"time"

	"github.com/whosthatknocking/abx/pkg/types"
)

func TestSavePendingApprovalDoesNotChangeActiveSession(t *testing.T) {
	repo := New().(*Repository)
	ctx := context.Background()
	conversationID := "direct:+1555"

	activeSessionID, err := repo.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active session id: %v", err)
	}
	otherSessionID, err := repo.RotateConversationSession(ctx, conversationID)
	if err != nil {
		t.Fatalf("rotate session: %v", err)
	}
	if _, err := repo.RotateConversationSession(ctx, conversationID); err != nil {
		t.Fatalf("rotate session again to change active session: %v", err)
	}
	currentActiveSessionID, err := repo.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		t.Fatalf("get current active session id: %v", err)
	}
	if currentActiveSessionID == otherSessionID {
		t.Fatalf("expected a distinct active session, got %q", currentActiveSessionID)
	}

	approval := types.PendingApproval{
		RequestID:      "req_1",
		ConversationID: conversationID,
		SessionID:      activeSessionID,
		Command:        "pwd",
		ProposedBy:     "+1555",
		Nonce:          "123",
		CreatedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(5 * time.Minute),
	}
	if err := repo.SavePendingApproval(ctx, conversationID, activeSessionID, approval); err != nil {
		t.Fatalf("save pending approval: %v", err)
	}

	activeAfterSave, err := repo.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active session after save: %v", err)
	}
	if activeAfterSave != currentActiveSessionID {
		t.Fatalf("expected active session to remain %q, got %q", currentActiveSessionID, activeAfterSave)
	}
}
