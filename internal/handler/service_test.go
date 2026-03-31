package handler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/whosthatknocking/abx/internal/agent"
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
	response     string
	err          error
	lastMessages []types.Message
}

func (a *fakeAgent) Chat(_ context.Context, messages []types.Message, _ []types.Tool) (types.AgentResponse, error) {
	a.lastMessages = append([]types.Message(nil), messages...)
	return types.AgentResponse{Text: a.response}, a.err
}

func (a *fakeAgent) Check(_ context.Context) error {
	return nil
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
				Fallback: config.ProviderConfig{Provider: "openai", Model: "llama3.2", BaseURL: "http://127.0.0.1:1234/v1"},
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
	if !strings.Contains(msgs.sent[0], "Primary: gpt-4o-mini (remote)") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Fallback: llama3.2 (local)") {
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

func TestRunWithoutCommandShowsUsage(t *testing.T) {
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

	err := svc.HandleMessage(context.Background(), types.IncomingEnvelope{
		ConversationID: "direct:+1555",
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "/run",
	})
	if err != nil {
		t.Fatalf("handle /run: %v", err)
	}
	if len(msgs.sent) != 1 {
		t.Fatalf("expected one sent message, got %d", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[0], "Usage: /run <command>") {
		t.Fatalf("unexpected /run help response: %q", msgs.sent[0])
	}
}

func TestDebugModeIncludesAgentLabelInConversationResponse(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Debug: config.DebugConfig{Enabled: true},
			Agent: config.AgentConfig{
				Primary: config.ProviderConfig{
					Provider: "openai",
					Model:    "qwen/qwen3-4b-2507",
					BaseURL:  "http://127.0.0.1:1234/v1",
				},
			},
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent:     &fakeAgent{response: "Hello from the model."},
		Executor:  &fakeExecutor{},
	})

	err := svc.HandleMessage(context.Background(), types.IncomingEnvelope{
		ConversationID: "direct:+1555",
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "hello",
	})
	if err != nil {
		t.Fatalf("handle conversational message: %v", err)
	}
	if len(msgs.sent) != 1 {
		t.Fatalf("expected one sent message, got %d", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[0], "[agent: openai / qwen/qwen3-4b-2507 (local)]") {
		t.Fatalf("expected debug agent label in response, got %q", msgs.sent[0])
	}
}

func TestDebugLabelIsNotStoredBackIntoHistory(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Debug: config.DebugConfig{Enabled: true},
			Agent: config.AgentConfig{
				Primary: config.ProviderConfig{
					Provider: "openai",
					Model:    "qwen/qwen3-4b-2507",
					BaseURL:  "http://127.0.0.1:1234/v1",
				},
			},
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent:     &fakeAgent{response: "Hello from the model."},
		Executor:  &fakeExecutor{},
	})

	ctx := context.Background()
	conversationID := "direct:+1555"
	if err := svc.HandleMessage(ctx, types.IncomingEnvelope{
		ConversationID: conversationID,
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "hello",
	}); err != nil {
		t.Fatalf("handle conversational message: %v", err)
	}

	sessionID, err := repo.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active session id: %v", err)
	}
	history, err := repo.GetHistory(ctx, conversationID, sessionID, 10)
	if err != nil {
		t.Fatalf("get history: %v", err)
	}
	if len(history) == 0 {
		t.Fatalf("expected history entries")
	}
	last := history[len(history)-1]
	if strings.Contains(last.Text, "[agent:") {
		t.Fatalf("expected stored assistant history without debug label, got %q", last.Text)
	}
}

func TestConversationUsesFallbackAgentWhenPrimaryFails(t *testing.T) {
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
		Agent:     agent.NewFallback(&fakeAgent{err: context.DeadlineExceeded}, &fakeAgent{response: "fallback reply"}),
		Executor:  &fakeExecutor{},
	})

	err := svc.HandleMessage(context.Background(), types.IncomingEnvelope{
		ConversationID: "direct:+1555",
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "hello",
	})
	if err != nil {
		t.Fatalf("handle conversational message: %v", err)
	}
	if len(msgs.sent) != 1 {
		t.Fatalf("expected one sent message, got %d", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[0], "fallback reply") {
		t.Fatalf("expected fallback response, got %q", msgs.sent[0])
	}
}

func TestConversationSummaryIsStoredAndPrependedForLongHistory(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	agentSpy := &fakeAgent{response: "hello"}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent:     agentSpy,
		Executor:  &fakeExecutor{},
	})

	ctx := context.Background()
	conversationID := "direct:+1555"
	sessionID, err := repo.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active session id: %v", err)
	}

	for i := 0; i < 25; i++ {
		if err := repo.SaveMessage(ctx, conversationID, sessionID, types.Message{
			ID:             mustID(),
			ConversationID: conversationID,
			SessionID:      sessionID,
			Sender:         "+1555",
			Role:           types.RoleUser,
			Kind:           types.MessageKindInbound,
			ChatType:       types.ChatTypeDirect,
			Text:           "message " + strings.Repeat("x", i+1),
			CreatedAt:      time.Now().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("save message %d: %v", i, err)
		}
	}

	if err := svc.HandleMessage(ctx, types.IncomingEnvelope{
		ConversationID: conversationID,
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "latest message",
	}); err != nil {
		t.Fatalf("handle conversational message: %v", err)
	}

	summary, err := repo.GetActiveConversationSummary(ctx, conversationID)
	if err != nil {
		t.Fatalf("get summary: %v", err)
	}
	if summary == "" {
		t.Fatal("expected non-empty conversation summary")
	}
	if len(agentSpy.lastMessages) == 0 {
		t.Fatal("expected agent to receive messages")
	}
	if agentSpy.lastMessages[0].Role != types.RoleSystem {
		t.Fatalf("expected first agent message to be system summary, got %s", agentSpy.lastMessages[0].Role)
	}
	if !strings.Contains(agentSpy.lastMessages[0].Text, "Conversation summary:") {
		t.Fatalf("expected conversation summary prefix, got %q", agentSpy.lastMessages[0].Text)
	}
	if !strings.Contains(agentSpy.lastMessages[0].Text, "message x") {
		t.Fatalf("expected older history to be summarized, got %q", agentSpy.lastMessages[0].Text)
	}
	if got := len(agentSpy.lastMessages); got != recentHistoryLimit+1 {
		t.Fatalf("expected summary plus %d recent messages, got %d", recentHistoryLimit, got)
	}
}
