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
