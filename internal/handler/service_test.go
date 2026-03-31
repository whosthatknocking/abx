package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/whosthatknocking/abx/internal/config"
	"github.com/whosthatknocking/abx/internal/repository/inmemory"
	"github.com/whosthatknocking/abx/pkg/types"
)

type fakeMessenger struct {
	sent []string
}

func (m *fakeMessenger) Start(ctx context.Context, handler func(types.IncomingEnvelope)) error {
	<-ctx.Done()
	return ctx.Err()
}

func (m *fakeMessenger) Send(_ context.Context, _ string, text string) error {
	m.sent = append(m.sent, text)
	return nil
}

type fakeAgent struct {
	response string
}

func (a *fakeAgent) Chat(_ context.Context, _ []types.Message, _ []types.Tool) (types.AgentResponse, error) {
	return types.AgentResponse{Text: a.response}, nil
}

type fakeExecutor struct {
	output string
}

func (e *fakeExecutor) Execute(_ context.Context, _ string) (string, error) {
	return e.output, nil
}

func TestConfigCommandIsHandledLocally(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Agent: config.AgentConfig{
				Primary:  config.ProviderConfig{Provider: "openai", Model: "gpt-4o-mini"},
				Fallback: config.ProviderConfig{Provider: "openai", Model: "llama3.2"},
			},
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent:     &fakeAgent{response: "ignored"},
		Executor:  &fakeExecutor{},
	})

	err := svc.HandleMessage(context.Background(), types.IncomingEnvelope{
		ConversationID: "direct:+1555",
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "/config",
	})
	if err != nil {
		t.Fatalf("handle /config: %v", err)
	}
	if len(msgs.sent) != 1 {
		t.Fatalf("expected one sent message, got %d", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[0], "Primary agent: openai / gpt-4o-mini") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
}

func TestResetStartsNewSession(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent:     &fakeAgent{response: "hello"},
		Executor:  &fakeExecutor{},
	})

	ctx := context.Background()
	conversationID := "direct:+1555"
	firstSession, err := repo.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		t.Fatalf("get initial session: %v", err)
	}
	if err := svc.HandleMessage(ctx, types.IncomingEnvelope{
		ConversationID: conversationID,
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "/reset",
	}); err != nil {
		t.Fatalf("handle /reset: %v", err)
	}
	secondSession, err := repo.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		t.Fatalf("get rotated session: %v", err)
	}
	if firstSession == secondSession {
		t.Fatalf("expected session rotation, got same session id %q", firstSession)
	}
}
