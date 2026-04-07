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

func TestSessionStateRoundTrip(t *testing.T) {
	repo := New().(*Repository)
	ctx := context.Background()
	conversationID := "direct:+1555"

	sessionID, err := repo.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active session id: %v", err)
	}

	if err := repo.SaveConversationSummary(ctx, conversationID, sessionID, "summary"); err != nil {
		t.Fatalf("save summary: %v", err)
	}
	if err := repo.SaveSessionPersona(ctx, conversationID, sessionID, "persona"); err != nil {
		t.Fatalf("save persona: %v", err)
	}
	if err := repo.SaveSessionFormat(ctx, conversationID, sessionID, "format"); err != nil {
		t.Fatalf("save format: %v", err)
	}
	if err := repo.SaveSessionThinkingMode(ctx, conversationID, sessionID, "disabled"); err != nil {
		t.Fatalf("save thinking mode: %v", err)
	}
	if err := repo.SaveSessionFallbackDisabled(ctx, conversationID, sessionID, true); err != nil {
		t.Fatalf("save fallback disabled: %v", err)
	}

	if got, _ := repo.GetActiveConversationSummary(ctx, conversationID); got != "summary" {
		t.Fatalf("unexpected summary %q", got)
	}
	if got, _ := repo.GetActiveSessionPersona(ctx, conversationID); got != "persona" {
		t.Fatalf("unexpected persona %q", got)
	}
	if got, _ := repo.GetActiveSessionFormat(ctx, conversationID); got != "format" {
		t.Fatalf("unexpected format %q", got)
	}
	if got, _ := repo.GetActiveSessionThinkingMode(ctx, conversationID); got != "disabled" {
		t.Fatalf("unexpected thinking mode %q", got)
	}
	if got, _ := repo.GetActiveSessionFallbackDisabled(ctx, conversationID); !got {
		t.Fatal("expected fallback to be disabled")
	}
}

func TestPendingApprovalLifecycle(t *testing.T) {
	repo := New().(*Repository)
	ctx := context.Background()
	conversationID := "direct:+1555"
	sessionID, err := repo.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active session id: %v", err)
	}

	first := types.PendingApproval{
		RequestID:      "req_1",
		ConversationID: conversationID,
		SessionID:      sessionID,
		Command:        "pwd",
		ProposedBy:     "+1555",
		Nonce:          "123",
		CreatedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(time.Minute),
	}
	second := first
	second.RequestID = "req_2"
	second.Command = "ls"

	if err := repo.SavePendingApproval(ctx, conversationID, sessionID, first); err != nil {
		t.Fatalf("save first approval: %v", err)
	}
	if err := repo.SavePendingApproval(ctx, conversationID, sessionID, second); err != nil {
		t.Fatalf("save second approval: %v", err)
	}

	active, err := repo.GetActivePendingApproval(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active approval: %v", err)
	}
	if active == nil || active.RequestID != "req_2" {
		t.Fatalf("unexpected active approval: %#v", active)
	}

	got, err := repo.GetPendingApproval(ctx, conversationID, "req_1")
	if err != nil {
		t.Fatalf("get first approval by id: %v", err)
	}
	if got == nil || got.Command != "pwd" {
		t.Fatalf("unexpected first approval lookup: %#v", got)
	}

	if err := repo.ClearPendingApproval(ctx, conversationID, "req_2"); err != nil {
		t.Fatalf("clear approval: %v", err)
	}
	active, err = repo.GetActivePendingApproval(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active approval after clear: %v", err)
	}
	if active != nil {
		t.Fatalf("expected no active approval after clear, got %#v", active)
	}
}

func TestMessageAttachmentsRoundTrip(t *testing.T) {
	repo := New().(*Repository)
	ctx := context.Background()
	conversationID := "direct:+1555"
	sessionID, err := repo.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active session id: %v", err)
	}

	msg := types.Message{
		ID:             "m1",
		ConversationID: conversationID,
		SessionID:      sessionID,
		Sender:         "+1555",
		Role:           types.RoleUser,
		Kind:           types.MessageKindInbound,
		ChatType:       types.ChatTypeDirect,
		Attachments: []types.Attachment{{
			Kind:        "image",
			ContentType: "image/png",
			FilePath:    "/tmp/test.png",
			FileName:    "test.png",
		}},
		CreatedAt: time.Now(),
	}
	if err := repo.SaveMessage(ctx, conversationID, sessionID, msg); err != nil {
		t.Fatalf("save message: %v", err)
	}

	history, err := repo.GetHistory(ctx, conversationID, sessionID, 10)
	if err != nil {
		t.Fatalf("get history: %v", err)
	}
	if len(history) != 1 || len(history[0].Attachments) != 1 {
		t.Fatalf("unexpected history %#v", history)
	}
	if history[0].Attachments[0].FilePath != "/tmp/test.png" {
		t.Fatalf("unexpected attachment %#v", history[0].Attachments[0])
	}
}
