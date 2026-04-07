package sqlite

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/whosthatknocking/abx/pkg/types"
)

func TestGetHistoryReturnsLatestMessagesInChronologicalOrder(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}

	dir := t.TempDir()
	repo, err := New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new sqlite repo: %v", err)
	}

	ctx := context.Background()
	conversationID := "direct:+1555"
	sessionID, err := repo.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active session: %v", err)
	}

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i, text := range []string{"one", "two", "three", "four", "five"} {
		err := repo.SaveMessage(ctx, conversationID, sessionID, types.Message{
			ID:             text,
			ConversationID: conversationID,
			SessionID:      sessionID,
			Sender:         "+1555",
			Role:           types.RoleUser,
			Kind:           types.MessageKindInbound,
			ChatType:       types.ChatTypeDirect,
			Text:           text,
			CreatedAt:      base.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatalf("save message %q: %v", text, err)
		}
	}

	history, err := repo.GetHistory(ctx, conversationID, sessionID, 3)
	if err != nil {
		t.Fatalf("get history: %v", err)
	}

	if len(history) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(history))
	}
	got := []string{history[0].Text, history[1].Text, history[2].Text}
	want := []string{"three", "four", "five"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected history order at %d: got %v want %v", i, got, want)
		}
	}
}

func TestSessionStateRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}

	dir := t.TempDir()
	repo, err := New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new sqlite repo: %v", err)
	}

	ctx := context.Background()
	conversationID := "direct:+1555"
	sessionID, err := repo.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active session: %v", err)
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
	if err := repo.SaveSessionThinkingMode(ctx, conversationID, sessionID, "enabled"); err != nil {
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
	if got, _ := repo.GetActiveSessionThinkingMode(ctx, conversationID); got != "enabled" {
		t.Fatalf("unexpected thinking mode %q", got)
	}
	if got, _ := repo.GetActiveSessionFallbackDisabled(ctx, conversationID); !got {
		t.Fatal("expected fallback to be disabled")
	}
}

func TestRotateConversationSessionClearsActivePendingApproval(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}

	dir := t.TempDir()
	repo, err := New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new sqlite repo: %v", err)
	}

	ctx := context.Background()
	conversationID := "direct:+1555"
	sessionID, err := repo.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active session: %v", err)
	}

	approval := types.PendingApproval{
		RequestID:      "req_1",
		ConversationID: conversationID,
		SessionID:      sessionID,
		Command:        "pwd",
		ProposedBy:     "+1555",
		Nonce:          "123",
		CreatedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(5 * time.Minute),
	}
	if err := repo.SavePendingApproval(ctx, conversationID, sessionID, approval); err != nil {
		t.Fatalf("save pending approval: %v", err)
	}

	newSessionID, err := repo.RotateConversationSession(ctx, conversationID)
	if err != nil {
		t.Fatalf("rotate session: %v", err)
	}
	if newSessionID == sessionID {
		t.Fatalf("expected a new session id, got %q", newSessionID)
	}

	active, err := repo.GetActivePendingApproval(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active pending approval: %v", err)
	}
	if active != nil {
		t.Fatalf("expected no active pending approval after rotate, got %#v", active)
	}
}

func TestPendingApprovalRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}

	dir := t.TempDir()
	repo, err := New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new sqlite repo: %v", err)
	}

	ctx := context.Background()
	conversationID := "direct:+1555"
	sessionID, err := repo.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active session: %v", err)
	}

	approval := types.PendingApproval{
		RequestID:      "req_1",
		ConversationID: conversationID,
		SessionID:      sessionID,
		Command:        "pwd",
		ProposedBy:     "+1555",
		Nonce:          "123",
		CreatedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(5 * time.Minute),
	}
	if err := repo.SavePendingApproval(ctx, conversationID, sessionID, approval); err != nil {
		t.Fatalf("save pending approval: %v", err)
	}

	got, err := repo.GetPendingApproval(ctx, conversationID, approval.RequestID)
	if err != nil {
		t.Fatalf("get pending approval: %v", err)
	}
	if got == nil || got.Command != "pwd" {
		t.Fatalf("unexpected pending approval: %#v", got)
	}

	active, err := repo.GetActivePendingApproval(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active pending approval: %v", err)
	}
	if active == nil || active.RequestID != approval.RequestID {
		t.Fatalf("unexpected active pending approval: %#v", active)
	}

	if err := repo.ClearPendingApproval(ctx, conversationID, approval.RequestID); err != nil {
		t.Fatalf("clear pending approval: %v", err)
	}
	active, err = repo.GetActivePendingApproval(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active pending approval after clear: %v", err)
	}
	if active != nil {
		t.Fatalf("expected no active approval after clear, got %#v", active)
	}
}

func TestMessageAttachmentsRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}

	dir := t.TempDir()
	repo, err := New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new sqlite repo: %v", err)
	}

	ctx := context.Background()
	conversationID := "direct:+1555"
	sessionID, err := repo.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active session: %v", err)
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
