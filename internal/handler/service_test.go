package handler

import (
	"context"
	"errors"
	"os/exec"
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
			Messaging: config.MessagingConfig{
				Provider: "signal-cli",
				SignalCLI: config.SignalCLIConfig{
					RPCMode: "json-rpc",
				},
			},
			Agent: config.AgentConfig{
				Primary:  config.ProviderConfig{Provider: "openai", Model: "gpt-4o-mini"},
				Fallback: config.ProviderConfig{Provider: "openai", Model: "llama3.2", BaseURL: "http://127.0.0.1:1234/v1"},
			},
			MCP: config.MCPConfig{
				Servers: []config.MCPServerConfig{
					{Name: "mcp/playwright", Enabled: true},
					{Name: "mcp/weather", Enabled: false},
				},
			},
			Debug:    config.DebugConfig{Enabled: true},
			Database: config.DatabaseConfig{Type: "sqlite"},
			Command: config.CommandConfig{
				TimeoutSeconds: 90,
				PolicyMode:     "allowlist",
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
	if !strings.Contains(msgs.sent[0], "Messaging: signal-cli / json-rpc") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Primary model: gpt-4o-mini") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Primary contract: openai-compatible") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Fallback model: llama3.2") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Fallback contract: openai-compatible") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "MCP: enabled (mcp/playwright)") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Storage: sqlite") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Command policy: allowlist") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Command timeout: 90s") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Debug: enabled") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Version: test") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
}

func TestConfigCommandOmitsFallbackAndUsesNormalizedDefaults(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Agent: config.AgentConfig{
				Primary: config.ProviderConfig{Model: "qwen/qwen3-4b-2507"},
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
	if strings.Contains(msgs.sent[0], "Fallback model:") {
		t.Fatalf("did not expect fallback details in /config response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Messaging: signal-cli / json-rpc") {
		t.Fatalf("expected normalized messaging defaults, got %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Primary contract: openai-compatible") {
		t.Fatalf("expected normalized contract, got %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "MCP: disabled") {
		t.Fatalf("expected normalized MCP state, got %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Storage: sqlite") {
		t.Fatalf("expected normalized storage, got %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Command policy: allowlist") {
		t.Fatalf("expected normalized policy mode, got %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Command timeout: 60s") {
		t.Fatalf("expected normalized command timeout, got %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Debug: disabled") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
}

func TestVersionCommandIncludesBuildMetadataWhenAvailable(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	svc := NewService(Options{
		Version:   "1.2.3",
		BuildInfo: "commit=abc123 built=2026-03-31T12:00:00Z",
		Config: &config.Config{
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
		Text:           "/version",
	})
	if err != nil {
		t.Fatalf("handle /version: %v", err)
	}
	if len(msgs.sent) != 1 {
		t.Fatalf("expected one sent message, got %d", len(msgs.sent))
	}
	if got := msgs.sent[0]; got != "abx version 1.2.3\nBuild: commit=abc123 built=2026-03-31T12:00:00Z" {
		t.Fatalf("unexpected /version response: %q", got)
	}
}

func TestVersionCommandOmitsBuildMetadataWhenUnavailable(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	svc := NewService(Options{
		Version: "dev",
		Config: &config.Config{
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
		Text:           "/version",
	})
	if err != nil {
		t.Fatalf("handle /version: %v", err)
	}
	if len(msgs.sent) != 1 {
		t.Fatalf("expected one sent message, got %d", len(msgs.sent))
	}
	if got := msgs.sent[0]; got != "abx version dev" {
		t.Fatalf("unexpected /version response: %q", got)
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

func TestExitStatusExtractsWrappedExitError(t *testing.T) {
	cmd := exec.Command("/bin/bash", "-c", "exit 9")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected command error")
	}

	got := exitStatus(errors.New("plain error"))
	if got != nil {
		t.Fatalf("expected nil exit status for plain error, got %v", *got)
	}

	wrapped := errors.Join(err)
	status := exitStatus(wrapped)
	if status == nil || *status != 9 {
		t.Fatalf("unexpected exit status: %#v", status)
	}
}

func TestNonApprovalReplyCancelsPendingApproval(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	agentSpy := &fakeAgent{response: "conversation reply"}
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
	if err := svc.HandleMessage(ctx, types.IncomingEnvelope{
		ConversationID: conversationID,
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "/run pwd",
	}); err != nil {
		t.Fatalf("handle /run pwd: %v", err)
	}

	approval, err := repo.GetActivePendingApproval(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active pending approval: %v", err)
	}
	if approval == nil {
		t.Fatal("expected pending approval to exist")
	}

	if err := svc.HandleMessage(ctx, types.IncomingEnvelope{
		ConversationID: conversationID,
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "actually tell me a joke",
	}); err != nil {
		t.Fatalf("handle conversational follow-up: %v", err)
	}

	approval, err = repo.GetActivePendingApproval(ctx, conversationID)
	if err != nil {
		t.Fatalf("get active pending approval after cancellation: %v", err)
	}
	if approval != nil {
		t.Fatalf("expected pending approval to be cleared, got %#v", approval)
	}
	if len(msgs.sent) != 2 {
		t.Fatalf("expected proposal plus conversation response, got %d messages", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[1], "conversation reply") {
		t.Fatalf("expected conversational reply after cancellation, got %q", msgs.sent[1])
	}
	if len(agentSpy.lastMessages) == 0 {
		t.Fatal("expected agent to handle follow-up message")
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
