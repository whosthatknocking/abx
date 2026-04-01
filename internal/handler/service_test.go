package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/whosthatknocking/abx/internal/agent"
	"github.com/whosthatknocking/abx/internal/config"
	"github.com/whosthatknocking/abx/internal/repository/inmemory"
	"github.com/whosthatknocking/abx/pkg/types"
)

type fakeMessenger struct {
	mu   sync.Mutex
	sent []string
}

func (m *fakeMessenger) Start(ctx context.Context, handler func(types.IncomingEnvelope)) error {
	<-ctx.Done()
	return ctx.Err()
}

func (m *fakeMessenger) Send(_ context.Context, _ string, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, text)
	return nil
}

type scriptedMessenger struct {
	envelopes []types.IncomingEnvelope
}

func (m *scriptedMessenger) Start(_ context.Context, handler func(types.IncomingEnvelope)) error {
	for _, env := range m.envelopes {
		handler(env)
	}
	return nil
}

func (m *scriptedMessenger) Send(_ context.Context, _ string, _ string) error {
	return nil
}

type fakeAgent struct {
	response      string
	err           error
	provider      string
	model         string
	endpointClass string
	integrations  []string
	inputTokens   int
	outputTokens  int
	totalTokens   int
	timeToFirst   float64
	lastMessages  []types.Message
}

func (a *fakeAgent) Chat(_ context.Context, messages []types.Message, _ []types.Tool) (types.AgentResponse, error) {
	a.lastMessages = append([]types.Message(nil), messages...)
	return types.AgentResponse{
		Text:          a.response,
		Provider:      a.provider,
		Model:         a.model,
		EndpointClass: a.endpointClass,
		Integrations:  append([]string(nil), a.integrations...),
		InputTokens:   a.inputTokens,
		OutputTokens:  a.outputTokens,
		TotalTokens:   a.totalTokens,
		TimeToFirst:   a.timeToFirst,
	}, a.err
}

func (a *fakeAgent) Check(_ context.Context) error {
	return nil
}

type fakeExecutor struct {
	output  string
	check   func(command string) error
	execute func(ctx context.Context, command string) (string, error)
}

func (e *fakeExecutor) Execute(ctx context.Context, command string) (string, error) {
	if e.execute != nil {
		return e.execute(ctx, command)
	}
	return e.output, nil
}

func (e *fakeExecutor) Check(command string) error {
	if e.check != nil {
		return e.check(command)
	}
	return nil
}

type blockingAgent struct {
	mu            sync.Mutex
	firstStarted  chan struct{}
	secondStarted chan struct{}
	releaseFirst  chan struct{}
	firstText     string
	secondText    string
}

func (a *blockingAgent) Chat(_ context.Context, messages []types.Message, _ []types.Tool) (types.AgentResponse, error) {
	latest := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == types.RoleUser {
			latest = messages[i].Text
			break
		}
	}
	switch latest {
	case a.firstText:
		select {
		case a.firstStarted <- struct{}{}:
		default:
		}
		<-a.releaseFirst
	case a.secondText:
		select {
		case a.secondStarted <- struct{}{}:
		default:
		}
	}
	return types.AgentResponse{Text: "ok"}, nil
}

func (a *blockingAgent) Check(_ context.Context) error {
	return nil
}

func TestStartProcessesDifferentConversationsConcurrently(t *testing.T) {
	agentSpy := &blockingAgent{
		firstStarted:  make(chan struct{}, 1),
		secondStarted: make(chan struct{}, 1),
		releaseFirst:  make(chan struct{}),
		firstText:     "conversation-a",
		secondText:    "conversation-b",
	}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo: inmemory.New(),
		Messenger: &scriptedMessenger{
			envelopes: []types.IncomingEnvelope{
				{ConversationID: "direct:+1555", Sender: "+1555", ChatType: types.ChatTypeDirect, Text: "conversation-a"},
				{ConversationID: "direct:+1666", Sender: "+1555", ChatType: types.ChatTypeDirect, Text: "conversation-b"},
			},
		},
		Agent:    agentSpy,
		Executor: &fakeExecutor{},
	})

	done := make(chan error, 1)
	go func() {
		done <- svc.Start(context.Background())
	}()

	select {
	case <-agentSpy.firstStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected first conversation to start")
	}

	select {
	case <-agentSpy.secondStarted:
		// expected: different conversation should proceed while first is blocked
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected second conversation to proceed concurrently")
	}

	close(agentSpy.releaseFirst)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("service start returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected service start to finish after unblocking first conversation")
	}
}

func TestHandleMessageSerializesSameConversation(t *testing.T) {
	agentSpy := &blockingAgent{
		firstStarted:  make(chan struct{}, 1),
		secondStarted: make(chan struct{}, 1),
		releaseFirst:  make(chan struct{}),
		firstText:     "first",
		secondText:    "second",
	}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      inmemory.New(),
		Messenger: &fakeMessenger{},
		Agent:     agentSpy,
		Executor:  &fakeExecutor{},
	})

	ctx := context.Background()
	doneFirst := make(chan error, 1)
	go func() {
		doneFirst <- svc.HandleMessage(ctx, types.IncomingEnvelope{
			ConversationID: "direct:+1555",
			Sender:         "+1555",
			ChatType:       types.ChatTypeDirect,
			Text:           "first",
		})
	}()

	select {
	case <-agentSpy.firstStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected first message to start")
	}

	doneSecond := make(chan error, 1)
	go func() {
		doneSecond <- svc.HandleMessage(ctx, types.IncomingEnvelope{
			ConversationID: "direct:+1555",
			Sender:         "+1555",
			ChatType:       types.ChatTypeDirect,
			Text:           "second",
		})
	}()

	select {
	case <-agentSpy.secondStarted:
		t.Fatal("did not expect second message in same conversation to start before first completed")
	case <-time.After(100 * time.Millisecond):
	}

	close(agentSpy.releaseFirst)
	select {
	case err := <-doneFirst:
		if err != nil {
			t.Fatalf("first handle returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected first message to finish after release")
	}

	select {
	case <-agentSpy.secondStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected second message to start after first completed")
	}

	select {
	case err := <-doneSecond:
		if err != nil {
			t.Fatalf("second handle returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected second message to finish")
	}
}

func TestReadOnlyControlBypassesBlockedAgentInSameConversation(t *testing.T) {
	agentSpy := &blockingAgent{
		firstStarted:  make(chan struct{}, 1),
		secondStarted: make(chan struct{}, 1),
		releaseFirst:  make(chan struct{}),
		firstText:     "first",
		secondText:    "/config",
	}
	msgs := &fakeMessenger{}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      inmemory.New(),
		Messenger: msgs,
		Agent:     agentSpy,
		Executor:  &fakeExecutor{},
	})

	ctx := context.Background()
	doneFirst := make(chan error, 1)
	go func() {
		doneFirst <- svc.HandleMessage(ctx, types.IncomingEnvelope{
			ConversationID: "direct:+1555",
			Sender:         "+1555",
			ChatType:       types.ChatTypeDirect,
			Text:           "first",
		})
	}()

	select {
	case <-agentSpy.firstStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected first message to start")
	}

	doneConfig := make(chan error, 1)
	go func() {
		doneConfig <- svc.HandleMessage(ctx, types.IncomingEnvelope{
			ConversationID: "direct:+1555",
			Sender:         "+1555",
			ChatType:       types.ChatTypeDirect,
			Text:           "/config",
		})
	}()

	select {
	case err := <-doneConfig:
		if err != nil {
			t.Fatalf("read-only control returned error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected /config to complete while agent request is still blocked")
	}

	select {
	case <-agentSpy.secondStarted:
		t.Fatal("did not expect /config to call the agent")
	default:
	}

	msgs.mu.Lock()
	got := append([]string(nil), msgs.sent...)
	msgs.mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected one immediate control response, got %d", len(got))
	}
	if !strings.Contains(got[0], "Version: test") {
		t.Fatalf("unexpected /config response: %q", got[0])
	}

	close(agentSpy.releaseFirst)
	select {
	case err := <-doneFirst:
		if err != nil {
			t.Fatalf("first handle returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected blocked conversation to finish after release")
	}
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
				Primary:  config.ProviderConfig{Provider: "openai", Model: "gpt-4o-mini", RequestTimeoutSeconds: 180},
				Fallback: config.ProviderConfig{Provider: "openai", Model: "llama3.2", BaseURL: "http://127.0.0.1:1234/v1", RequestTimeoutSeconds: 45},
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
	if !strings.Contains(msgs.sent[0], "Primary request timeout: 180s") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Fallback model: llama3.2") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Fallback contract: openai-compatible") {
		t.Fatalf("unexpected /config response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Fallback request timeout: 45s") {
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
	if !strings.Contains(msgs.sent[0], "Primary request timeout: 60s") {
		t.Fatalf("expected normalized primary timeout, got %q", msgs.sent[0])
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

func TestAgentsListCommandShowsConfiguredAgents(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Agent: config.AgentConfig{
				Primary:  config.ProviderConfig{Provider: "openai", Model: "qwen/qwen3-4b-2507", BaseURL: "http://127.0.0.1:1234/v1"},
				Fallback: config.ProviderConfig{Provider: "openai", Model: "gpt-5-nano"},
			},
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent:     &fakeAgent{response: "ignored"},
		Executor:  &fakeExecutor{},
	})

	if err := svc.HandleMessage(context.Background(), types.IncomingEnvelope{
		ConversationID: "direct:+1555",
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "/agents list",
	}); err != nil {
		t.Fatalf("handle /agents list: %v", err)
	}
	if !strings.Contains(msgs.sent[0], "Configured agents:") ||
		!strings.Contains(msgs.sent[0], "- primary: qwen/qwen3-4b-2507 (openai-compatible)") ||
		!strings.Contains(msgs.sent[0], "- fallback: gpt-5-nano (openai-compatible)") {
		t.Fatalf("unexpected /agents list response: %q", msgs.sent[0])
	}
}

func TestAgentsUsageBypassesBlockedAgentInSameConversation(t *testing.T) {
	agentSpy := &blockingAgent{
		firstStarted:  make(chan struct{}, 1),
		secondStarted: make(chan struct{}, 1),
		releaseFirst:  make(chan struct{}),
		firstText:     "first",
		secondText:    "/agents",
	}
	msgs := &fakeMessenger{}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      inmemory.New(),
		Messenger: msgs,
		Agent:     agentSpy,
		Executor:  &fakeExecutor{},
	})

	ctx := context.Background()
	doneFirst := make(chan error, 1)
	go func() {
		doneFirst <- svc.HandleMessage(ctx, types.IncomingEnvelope{
			ConversationID: "direct:+1555",
			Sender:         "+1555",
			ChatType:       types.ChatTypeDirect,
			Text:           "first",
		})
	}()

	select {
	case <-agentSpy.firstStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected first message to start")
	}

	doneUsage := make(chan error, 1)
	go func() {
		doneUsage <- svc.HandleMessage(ctx, types.IncomingEnvelope{
			ConversationID: "direct:+1555",
			Sender:         "+1555",
			ChatType:       types.ChatTypeDirect,
			Text:           "/agents",
		})
	}()

	select {
	case err := <-doneUsage:
		if err != nil {
			t.Fatalf("usage command returned error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected /agents usage to complete while agent request is blocked")
	}

	msgs.mu.Lock()
	got := append([]string(nil), msgs.sent...)
	msgs.mu.Unlock()
	if len(got) != 1 || !strings.Contains(got[0], "Usage: /agents <list|status|switch>") {
		t.Fatalf("unexpected /agents usage response: %#v", got)
	}

	close(agentSpy.releaseFirst)
	select {
	case err := <-doneFirst:
		if err != nil {
			t.Fatalf("first handle returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected blocked conversation to finish after release")
	}
}

func TestAgentsStatusCommandChecksConfiguredAgents(t *testing.T) {
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer okServer.Close()

	failingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer failingServer.Close()

	repo := inmemory.New()
	msgs := &fakeMessenger{}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Agent: config.AgentConfig{
				Primary:  config.ProviderConfig{Provider: "openai", Model: "qwen/qwen3-4b-2507", BaseURL: okServer.URL + "/v1"},
				Fallback: config.ProviderConfig{Provider: "openai", Model: "gpt-5-nano", BaseURL: failingServer.URL + "/v1"},
			},
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent:     &fakeAgent{response: "ignored"},
		Executor:  &fakeExecutor{},
	})

	if err := svc.HandleMessage(context.Background(), types.IncomingEnvelope{
		ConversationID: "direct:+1555",
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "/agents status",
	}); err != nil {
		t.Fatalf("handle /agents status: %v", err)
	}
	if !strings.Contains(msgs.sent[0], "Agent status:") ||
		!strings.Contains(msgs.sent[0], "- primary: qwen/qwen3-4b-2507 (openai-compatible): ok") ||
		!strings.Contains(msgs.sent[0], "- fallback: gpt-5-nano (openai-compatible): error:") {
		t.Fatalf("unexpected /agents status response: %q", msgs.sent[0])
	}
}

func TestAgentsSwitchSwitchesPrimaryAndFallback(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	fallbackAgent := &fakeAgent{
		response:      "fallback reply",
		provider:      "openai",
		model:         "gpt-5-nano",
		endpointClass: "remote",
	}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Debug: config.DebugConfig{Enabled: true},
			Agent: config.AgentConfig{
				Primary:  config.ProviderConfig{Provider: "openai", Model: "qwen/qwen3-4b-2507", BaseURL: "http://127.0.0.1:1234/v1"},
				Fallback: config.ProviderConfig{Provider: "openai", Model: "gpt-5-nano"},
			},
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent: agent.NewFallback(
			&fakeAgent{response: "primary reply", provider: "openai", model: "qwen/qwen3-4b-2507", endpointClass: "local"},
			fallbackAgent,
		),
		Executor: &fakeExecutor{},
	})

	ctx := context.Background()
	if err := svc.HandleMessage(ctx, types.IncomingEnvelope{
		ConversationID: "direct:+1555",
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "/agents switch",
	}); err != nil {
		t.Fatalf("handle /agents switch: %v", err)
	}
	if len(msgs.sent) != 1 {
		t.Fatalf("expected one sent message after swap, got %d", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[0], "Swapped active agent order.") {
		t.Fatalf("unexpected swap response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Primary model: gpt-5-nano") {
		t.Fatalf("expected swapped primary in response, got %q", msgs.sent[0])
	}

	if err := svc.HandleMessage(ctx, types.IncomingEnvelope{
		ConversationID: "direct:+1555",
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "/config",
	}); err != nil {
		t.Fatalf("handle /config after swap: %v", err)
	}
	if !strings.Contains(msgs.sent[1], "Primary model: gpt-5-nano") {
		t.Fatalf("expected swapped primary in /config, got %q", msgs.sent[1])
	}
	if !strings.Contains(msgs.sent[1], "Fallback model: qwen/qwen3-4b-2507") {
		t.Fatalf("expected swapped fallback in /config, got %q", msgs.sent[1])
	}

	if err := svc.HandleMessage(ctx, types.IncomingEnvelope{
		ConversationID: "direct:+1555",
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "hello",
	}); err != nil {
		t.Fatalf("handle conversation after swap: %v", err)
	}
	if !strings.Contains(msgs.sent[2], "fallback reply") {
		t.Fatalf("expected swapped primary agent response, got %q", msgs.sent[2])
	}
	if !strings.Contains(msgs.sent[2], "[agent: openai / gpt-5-nano (remote)]") {
		t.Fatalf("expected debug label for swapped primary, got %q", msgs.sent[2])
	}
}

func TestAgentsSwitchRequiresFallback(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Agent: config.AgentConfig{
				Primary: config.ProviderConfig{Provider: "openai", Model: "qwen/qwen3-4b-2507", BaseURL: "http://127.0.0.1:1234/v1"},
			},
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent:     &fakeAgent{response: "primary reply"},
		Executor:  &fakeExecutor{},
	})

	if err := svc.HandleMessage(context.Background(), types.IncomingEnvelope{
		ConversationID: "direct:+1555",
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "/agents switch",
	}); err != nil {
		t.Fatalf("handle /agents switch without fallback: %v", err)
	}
	if !strings.Contains(msgs.sent[0], "Fallback agent is not configured.") {
		t.Fatalf("unexpected no-fallback response: %q", msgs.sent[0])
	}
}

func TestGroupMentionRunCommandIsHandledLocally(t *testing.T) {
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
		Agent:     &fakeAgent{response: "ignored"},
		Executor:  &fakeExecutor{},
	})

	err := svc.HandleMessage(context.Background(), types.IncomingEnvelope{
		ConversationID: "group:test",
		Sender:         "+1555",
		ChatType:       types.ChatTypeGroup,
		MentionedBot:   true,
		Text:           "@abx /run pwd",
		NormalizedText: "/run pwd",
	})
	if err != nil {
		t.Fatalf("handle group /run pwd: %v", err)
	}
	if len(msgs.sent) != 1 {
		t.Fatalf("expected one sent message, got %d", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[0], "Command:\npwd") {
		t.Fatalf("unexpected group /run response: %q", msgs.sent[0])
	}
}

func TestGroupMentionRunIntentWithNonAtPrefixIsHandledLocally(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	agentSpy := &fakeAgent{response: "COMMAND: pwd\nWHY: Shows the current working directory."}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent:     agentSpy,
		Executor: &fakeExecutor{
			check: func(command string) error {
				if command == "pwd" {
					return nil
				}
				return errors.New("command blocked by policy: no allow rule matched in allowlist mode")
			},
		},
	})

	err := svc.HandleMessage(context.Background(), types.IncomingEnvelope{
		ConversationID: "group:test",
		Sender:         "+1555",
		ChatType:       types.ChatTypeGroup,
		MentionedBot:   true,
		Text:           "\uFFFC /run what is your current directory",
		NormalizedText: "/run what is your current directory",
	})
	if err != nil {
		t.Fatalf("handle group /run intent: %v", err)
	}
	if len(msgs.sent) != 1 {
		t.Fatalf("expected one sent message, got %d", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[0], "Command:\npwd") {
		t.Fatalf("unexpected group /run intent response: %q", msgs.sent[0])
	}
	if len(agentSpy.lastMessages) == 0 {
		t.Fatal("expected agent recommendation path for group /run intent")
	}
}

func TestGroupMentionApprovalWithPrefixIsHandledLocally(t *testing.T) {
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
		Agent:     &fakeAgent{response: "ignored"},
		Executor: &fakeExecutor{
			output: "/tmp",
			check: func(command string) error {
				if command == "pwd" {
					return nil
				}
				return errors.New("command blocked by policy: no allow rule matched in allowlist mode")
			},
		},
	})

	ctx := context.Background()
	conversationID := "group:test"
	if err := svc.HandleMessage(ctx, types.IncomingEnvelope{
		ConversationID: conversationID,
		Sender:         "+1555",
		ChatType:       types.ChatTypeGroup,
		MentionedBot:   true,
		Text:           "@abx /run pwd",
		NormalizedText: "/run pwd",
	}); err != nil {
		t.Fatalf("handle group /run pwd: %v", err)
	}

	approval, err := repo.GetActivePendingApproval(ctx, conversationID)
	if err != nil {
		t.Fatalf("get pending approval: %v", err)
	}
	if approval == nil {
		t.Fatal("expected pending approval")
	}

	if err := svc.HandleMessage(ctx, types.IncomingEnvelope{
		ConversationID: conversationID,
		Sender:         "+1555",
		ChatType:       types.ChatTypeGroup,
		MentionedBot:   true,
		Text:           "\uFFFC YES " + approval.Nonce,
		NormalizedText: "YES " + approval.Nonce,
	}); err != nil {
		t.Fatalf("handle group approval: %v", err)
	}
	if len(msgs.sent) != 2 {
		t.Fatalf("expected proposal and command result, got %d messages", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[1], "Command completed.") {
		t.Fatalf("unexpected group approval response: %q", msgs.sent[1])
	}
}

func TestGroupMentionControlCommandIsHandledLocally(t *testing.T) {
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
		Agent:     &fakeAgent{response: "ignored"},
		Executor:  &fakeExecutor{},
	})

	err := svc.HandleMessage(context.Background(), types.IncomingEnvelope{
		ConversationID: "group:test",
		Sender:         "+1555",
		ChatType:       types.ChatTypeGroup,
		MentionedBot:   true,
		Text:           "@abx /help",
		NormalizedText: "/help",
	})
	if err != nil {
		t.Fatalf("handle group /help: %v", err)
	}
	if len(msgs.sent) != 1 {
		t.Fatalf("expected one sent message, got %d", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[0], "Available message types:") {
		t.Fatalf("unexpected group /help response: %q", msgs.sent[0])
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

func TestHelpCommandSummarizesAvailableTypes(t *testing.T) {
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
		Agent:     &fakeAgent{response: "ignored"},
		Executor:  &fakeExecutor{},
	})

	err := svc.HandleMessage(context.Background(), types.IncomingEnvelope{
		ConversationID: "direct:+1555",
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "/help",
	})
	if err != nil {
		t.Fatalf("handle /help: %v", err)
	}
	if len(msgs.sent) != 1 {
		t.Fatalf("expected one sent message, got %d", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[0], "Available message types:") {
		t.Fatalf("unexpected /help response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "- /run <command-or-intent>:") {
		t.Fatalf("unexpected /help response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "- /help") {
		t.Fatalf("unexpected /help response: %q", msgs.sent[0])
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
	if !strings.Contains(msgs.sent[0], "Usage: /run <command-or-intent>") {
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
		Agent: &fakeAgent{
			response: "Hello from the model.",
		},
		Executor: &fakeExecutor{},
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

func TestConversationPathPrependsSystemGuidance(t *testing.T) {
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

	err := svc.HandleMessage(context.Background(), types.IncomingEnvelope{
		ConversationID: "direct:+1555",
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "Test",
	})
	if err != nil {
		t.Fatalf("handle conversation: %v", err)
	}
	if len(agentSpy.lastMessages) == 0 {
		t.Fatal("expected conversation messages to be sent to agent")
	}
	if agentSpy.lastMessages[0].Role != types.RoleSystem {
		t.Fatalf("expected first conversation message to be system, got %#v", agentSpy.lastMessages[0])
	}
	if !strings.Contains(agentSpy.lastMessages[0].Text, "Do not invent approval tokens") {
		t.Fatalf("expected conversation guardrail in system prompt, got %q", agentSpy.lastMessages[0].Text)
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

func TestDebugModeUsesFallbackResponderMetadata(t *testing.T) {
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
		Agent: agent.NewFallback(
			&fakeAgent{err: context.DeadlineExceeded},
			&fakeAgent{response: "fallback reply", provider: "openai", model: "gpt-5-nano", endpointClass: "remote"},
		),
		Executor: &fakeExecutor{},
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
	if !strings.Contains(msgs.sent[0], "[agent: openai / gpt-5-nano (remote)]") {
		t.Fatalf("expected fallback responder label in response, got %q", msgs.sent[0])
	}
}

func TestDebugModeIncludesUsedMCPIntegrations(t *testing.T) {
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
		Agent: &fakeAgent{
			response:      "Hello from the model.",
			provider:      "openai",
			model:         "qwen/qwen3-4b-2507",
			endpointClass: "local",
			integrations:  []string{"mcp/playwright"},
		},
		Executor: &fakeExecutor{},
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
	if !strings.Contains(msgs.sent[0], "mcp: mcp/playwright") {
		t.Fatalf("expected MCP integration label in debug response, got %q", msgs.sent[0])
	}
}

func TestDebugModeIncludesAgentStats(t *testing.T) {
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
		Agent: &fakeAgent{
			response:      "Hello from the model.",
			provider:      "openai",
			model:         "qwen/qwen3-4b-2507",
			endpointClass: "local",
			inputTokens:   120,
			outputTokens:  35,
			totalTokens:   155,
			timeToFirst:   0.84,
		},
		Executor: &fakeExecutor{},
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
	if !strings.Contains(msgs.sent[0], "stats: tokens: in=120 out=35 total=155 | ttft: 0.84s") {
		t.Fatalf("expected stats in debug response, got %q", msgs.sent[0])
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
		Executor: &fakeExecutor{
			check: func(command string) error {
				if command == "pwd" {
					return nil
				}
				return errors.New("blocked")
			},
		},
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

func TestRunIntentUsesAgentRecommendedCommand(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	agentSpy := &fakeAgent{response: "COMMAND: whoami\nWHY: Shows the current user account."}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent:     agentSpy,
		Executor: &fakeExecutor{
			check: func(command string) error {
				if command == "whoami" {
					return nil
				}
				return errors.New("command blocked by policy: no allow rule matched in allowlist mode")
			},
		},
	})

	err := svc.HandleMessage(context.Background(), types.IncomingEnvelope{
		ConversationID: "direct:+1555",
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "/run show the current user",
	})
	if err != nil {
		t.Fatalf("handle /run intent: %v", err)
	}
	if len(msgs.sent) != 1 {
		t.Fatalf("expected one sent message, got %d", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[0], "Command:\nwhoami") {
		t.Fatalf("expected recommended command in response, got %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Why:\nShows the current user account.") {
		t.Fatalf("expected recommendation reason in response, got %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Reply with:\nYES ") {
		t.Fatalf("expected approval token in response, got %q", msgs.sent[0])
	}
	if len(agentSpy.lastMessages) == 0 {
		t.Fatal("expected agent to be invoked for /run intent")
	}
	if agentSpy.lastMessages[0].Role != types.RoleSystem || !strings.Contains(agentSpy.lastMessages[0].Text, "Return exactly this format:") {
		t.Fatalf("expected run recommendation system prompt, got %#v", agentSpy.lastMessages[0])
	}
}

func TestRunIntentBlockedRecommendationReturnsExplanation(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	agentSpy := &fakeAgent{response: "COMMAND: rm -rf /\nWHY: Deletes everything."}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent:     agentSpy,
		Executor: &fakeExecutor{
			check: func(command string) error {
				if command == "rm -rf /" {
					return errors.New("command blocked by policy: matched deny rule(s): deny-rm-root")
				}
				return errors.New("command blocked by policy: no allow rule matched in allowlist mode")
			},
		},
	})

	err := svc.HandleMessage(context.Background(), types.IncomingEnvelope{
		ConversationID: "direct:+1555",
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "/run delete everything",
	})
	if err != nil {
		t.Fatalf("handle blocked /run intent: %v", err)
	}
	if len(msgs.sent) != 1 {
		t.Fatalf("expected one sent message, got %d", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[0], "Command not allowed by the current policy.") {
		t.Fatalf("expected blocked recommendation explanation, got %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Suggested command:\nrm -rf /") {
		t.Fatalf("expected suggested command in blocked recommendation, got %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "deny-rm-root") {
		t.Fatalf("expected deny rule details, got %q", msgs.sent[0])
	}
}

func TestConversationCommandProposalCreatesRealPendingApproval(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	agentSpy := &fakeAgent{response: "I can't fetch real-time prices directly in this chat. If you want me to pull the latest TSLA price, I can run a command to fetch it from Yahoo Finance.\n\nCommand:\ncurl -s \"https://query1.finance.yahoo.com/v7/finance/quote?symbols=TSLA\"\n\nReply with:\nYES 0d4f8a"}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent:     agentSpy,
		Executor: &fakeExecutor{
			output: "ok",
			check: func(command string) error {
				if command == `curl -s "https://query1.finance.yahoo.com/v7/finance/quote?symbols=TSLA"` {
					return nil
				}
				return errors.New("command blocked by policy: no allow rule matched in allowlist mode")
			},
		},
	})

	ctx := context.Background()
	conversationID := "direct:+1555"
	if err := svc.HandleMessage(ctx, types.IncomingEnvelope{
		ConversationID: conversationID,
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "what is TSLA stock price",
	}); err != nil {
		t.Fatalf("handle conversation command proposal: %v", err)
	}

	approval, err := repo.GetActivePendingApproval(ctx, conversationID)
	if err != nil {
		t.Fatalf("get pending approval: %v", err)
	}
	if approval == nil {
		t.Fatal("expected real pending approval to be created")
	}
	if approval.Command != `curl -s "https://query1.finance.yahoo.com/v7/finance/quote?symbols=TSLA"` {
		t.Fatalf("unexpected proposed command: %q", approval.Command)
	}
	if len(msgs.sent) != 1 {
		t.Fatalf("expected one sent message, got %d", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[0], "Command:\ncurl -s \"https://query1.finance.yahoo.com/v7/finance/quote?symbols=TSLA\"") {
		t.Fatalf("unexpected proposal response: %q", msgs.sent[0])
	}
	if strings.Contains(msgs.sent[0], "YES 0d4f8a") {
		t.Fatalf("expected app-generated approval token, got model token in %q", msgs.sent[0])
	}
}

func TestConversationCommandProposalBlockedReturnsPolicyExplanation(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	agentSpy := &fakeAgent{response: "I can run a command for that.\n\nCommand:\nrm -rf /\n\nReply with:\nYES deadbe"}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent:     agentSpy,
		Executor: &fakeExecutor{
			check: func(command string) error {
				if command == "rm -rf /" {
					return errors.New("command blocked by policy: matched deny rule(s): deny-rm-root")
				}
				return nil
			},
		},
	})

	if err := svc.HandleMessage(context.Background(), types.IncomingEnvelope{
		ConversationID: "direct:+1555",
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "delete everything",
	}); err != nil {
		t.Fatalf("handle blocked conversation command proposal: %v", err)
	}
	if len(msgs.sent) != 1 {
		t.Fatalf("expected one sent message, got %d", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[0], "Command not allowed by the current policy.") {
		t.Fatalf("unexpected blocked conversation proposal response: %q", msgs.sent[0])
	}
	if !strings.Contains(msgs.sent[0], "Suggested command:\nrm -rf /") {
		t.Fatalf("expected suggested command in blocked conversation proposal, got %q", msgs.sent[0])
	}
}

func TestRunExactBlockedCommandDoesNotFallBackToRecommendation(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	agentSpy := &fakeAgent{response: "COMMAND: whoami\nWHY: Shows the current user."}
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent:     agentSpy,
		Executor: &fakeExecutor{
			check: func(command string) error {
				if command == "rm -rf /" {
					return errors.New("command blocked by policy: matched deny rule(s): deny-rm-root")
				}
				return nil
			},
		},
	})

	err := svc.HandleMessage(context.Background(), types.IncomingEnvelope{
		ConversationID: "direct:+1555",
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "/run rm -rf /",
	})
	if err != nil {
		t.Fatalf("handle blocked exact /run: %v", err)
	}
	if len(msgs.sent) != 1 {
		t.Fatalf("expected one sent message, got %d", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[0], "command blocked by policy: matched deny rule(s): deny-rm-root") {
		t.Fatalf("unexpected blocked exact command response: %q", msgs.sent[0])
	}
	if len(agentSpy.lastMessages) != 0 {
		t.Fatal("did not expect agent recommendation path for blocked exact command")
	}
}

func TestCommandOutputStoredInHistoryIsTruncatedForAgentContext(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	longOutput := strings.Repeat("x", commandContextChars+200)
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent:     &fakeAgent{response: "ignored"},
		Executor: &fakeExecutor{
			output: longOutput,
			check: func(command string) error {
				if command == "pwd" {
					return nil
				}
				return errors.New("blocked")
			},
		},
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
		t.Fatalf("get pending approval: %v", err)
	}
	if approval == nil {
		t.Fatal("expected pending approval")
	}

	if err := svc.HandleMessage(ctx, types.IncomingEnvelope{
		ConversationID: conversationID,
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "YES " + approval.Nonce,
	}); err != nil {
		t.Fatalf("approve command: %v", err)
	}

	if len(msgs.sent) != 2 {
		t.Fatalf("expected proposal and command result, got %d messages", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[1], longOutput) {
		t.Fatalf("expected full output in displayed command result, got %q", msgs.sent[1])
	}

	sessionID, err := repo.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		t.Fatalf("get session id: %v", err)
	}
	history, err := repo.GetHistory(ctx, conversationID, sessionID, 10)
	if err != nil {
		t.Fatalf("get history: %v", err)
	}
	if len(history) == 0 {
		t.Fatal("expected history entries")
	}
	last := history[len(history)-1]
	if !strings.Contains(last.Text, "[command output truncated for conversation context]") {
		t.Fatalf("expected stored command result to note truncation, got %q", last.Text)
	}
	if len(last.Text) >= len(msgs.sent[1]) {
		t.Fatalf("expected stored command result to be shorter than displayed result")
	}
}

func TestApprovalRechecksPolicyBeforeExecution(t *testing.T) {
	repo := inmemory.New()
	msgs := &fakeMessenger{}
	allowed := true
	executed := false
	svc := NewService(Options{
		Version: "test",
		Config: &config.Config{
			Security: config.SecurityConfig{TrustedNumbers: []string{"+1555"}},
		},
		Repo:      repo,
		Auditor:   nil,
		Messenger: msgs,
		Agent:     &fakeAgent{response: "ignored"},
		Executor: &fakeExecutor{
			check: func(command string) error {
				if command != "pwd" {
					return errors.New("command blocked by policy: no allow rule matched in allowlist mode")
				}
				if !allowed {
					return errors.New("command blocked by policy: matched deny rule(s): deny-pwd")
				}
				return nil
			},
			execute: func(_ context.Context, command string) (string, error) {
				executed = true
				return "/tmp", nil
			},
		},
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
		t.Fatalf("get pending approval: %v", err)
	}
	if approval == nil {
		t.Fatal("expected pending approval")
	}

	allowed = false
	if err := svc.HandleMessage(ctx, types.IncomingEnvelope{
		ConversationID: conversationID,
		Sender:         "+1555",
		ChatType:       types.ChatTypeDirect,
		Text:           "YES " + approval.Nonce,
	}); err != nil {
		t.Fatalf("approve command after policy change: %v", err)
	}

	if executed {
		t.Fatal("did not expect command execution after policy recheck failure")
	}
	if len(msgs.sent) != 2 {
		t.Fatalf("expected proposal and policy failure response, got %d messages", len(msgs.sent))
	}
	if !strings.Contains(msgs.sent[1], "command blocked by policy: matched deny rule(s): deny-pwd") {
		t.Fatalf("unexpected approval recheck failure response: %q", msgs.sent[1])
	}
	approval, err = repo.GetActivePendingApproval(ctx, conversationID)
	if err != nil {
		t.Fatalf("get pending approval after recheck failure: %v", err)
	}
	if approval != nil {
		t.Fatalf("expected pending approval to be cleared, got %#v", approval)
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
		t.Fatalf("expected first agent message to be system guidance, got %s", agentSpy.lastMessages[0].Role)
	}
	if !strings.Contains(agentSpy.lastMessages[0].Text, "Do not invent approval tokens") {
		t.Fatalf("expected conversation guidance prompt, got %q", agentSpy.lastMessages[0].Text)
	}
	if len(agentSpy.lastMessages) < 2 || agentSpy.lastMessages[1].Role != types.RoleSystem {
		t.Fatalf("expected second agent message to be system summary, got %#v", agentSpy.lastMessages)
	}
	if !strings.Contains(agentSpy.lastMessages[1].Text, "Conversation summary:") {
		t.Fatalf("expected conversation summary prefix, got %q", agentSpy.lastMessages[1].Text)
	}
	if !strings.Contains(agentSpy.lastMessages[1].Text, "message x") {
		t.Fatalf("expected older history to be summarized, got %q", agentSpy.lastMessages[1].Text)
	}
	if got := len(agentSpy.lastMessages); got != recentHistoryLimit+2 {
		t.Fatalf("expected guidance plus summary plus %d recent messages, got %d", recentHistoryLimit, got)
	}
}
