package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/whosthatknocking/abx/internal/agent"
	"github.com/whosthatknocking/abx/internal/config"
	"github.com/whosthatknocking/abx/pkg/types"
)

type Provider struct {
	cfg    config.ProviderConfig
	client *http.Client
}

var _ agent.Provider = (*Provider)(nil)

// New returns an OpenAI-compatible provider.
//
// The same implementation is used for both remote OpenAI-style APIs and local
// OpenAI-compatible endpoints such as LM Studio. When local MCP integrations
// are enabled, the provider switches to LM Studio's native chat endpoint.
func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg: cfg,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (p *Provider) Chat(ctx context.Context, messages []types.Message, _ []types.Tool) (types.AgentResponse, error) {
	body := p.chatRequestBody(messages)
	raw, err := json.Marshal(body)
	if err != nil {
		return types.AgentResponse{}, err
	}

	url := p.chatURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return types.AgentResponse{}, err
	}
	if p.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return types.AgentResponse{}, fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		bodyText, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if trimmed := strings.TrimSpace(string(bodyText)); trimmed != "" {
			return types.AgentResponse{}, p.formatAPIError(resp.StatusCode, resp.Status, trimmed)
		}
		return types.AgentResponse{}, fmt.Errorf("openai request failed: %s", resp.Status)
	}
	if p.usesNativeLMStudioChat() {
		return p.decodeLMStudioChatResponse(resp.Body)
	}
	return p.decodeOpenAIChatResponse(resp.Body)
}

func (p *Provider) decodeOpenAIChatResponse(body io.Reader) (types.AgentResponse, error) {
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(body).Decode(&decoded); err != nil {
		return types.AgentResponse{}, fmt.Errorf("decode openai response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return types.AgentResponse{}, fmt.Errorf("openai response did not contain any choices")
	}
	return types.AgentResponse{
		Text:          strings.TrimSpace(decoded.Choices[0].Message.Content),
		Provider:      strings.TrimSpace(p.cfg.Provider),
		Model:         strings.TrimSpace(p.cfg.Model),
		EndpointClass: endpointClass(p.cfg.BaseURL),
	}, nil
}

func (p *Provider) decodeLMStudioChatResponse(body io.Reader) (types.AgentResponse, error) {
	var decoded struct {
		Output []struct {
			Type     string `json:"type"`
			Content  string `json:"content"`
			Reason   string `json:"reason"`
			Tool     string `json:"tool"`
			Provider struct {
				Type string `json:"type"`
			} `json:"provider_info"`
		} `json:"output"`
	}
	if err := json.NewDecoder(body).Decode(&decoded); err != nil {
		return types.AgentResponse{}, fmt.Errorf("decode LM Studio response: %w", err)
	}

	parts := make([]string, 0, len(decoded.Output))
	for _, item := range decoded.Output {
		if item.Type == "message" {
			text := strings.TrimSpace(item.Content)
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	if len(parts) > 0 {
		return types.AgentResponse{
			Text:          strings.Join(parts, "\n\n"),
			Provider:      strings.TrimSpace(p.cfg.Provider),
			Model:         strings.TrimSpace(p.cfg.Model),
			EndpointClass: endpointClass(p.cfg.BaseURL),
		}, nil
	}
	for _, item := range decoded.Output {
		if item.Type == "invalid_tool_call" {
			return types.AgentResponse{}, fmt.Errorf("LM Studio rejected tool call: %s", strings.TrimSpace(item.Reason))
		}
	}
	return types.AgentResponse{}, fmt.Errorf("LM Studio response did not contain any message output")
}

func (p *Provider) Check(ctx context.Context) error {
	// Prefer checking the specific configured model when the backend supports
	// it, but fall back to the model list for OpenAI-compatible servers that do
	// not expose GET /models/{id}.
	url := strings.TrimRight(p.baseURL(), "/") + "/models/" + p.cfg.Model
	if err := p.checkURL(ctx, url); err == nil {
		return nil
	}
	fallbackURL := strings.TrimRight(p.baseURL(), "/") + "/models"
	if err := p.checkURL(ctx, fallbackURL); err != nil {
		return fmt.Errorf("openai connectivity check failed for %s and %s: %w", url, fallbackURL, err)
	}
	return nil
}

func (p *Provider) checkURL(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if p.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("openai connectivity check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("openai connectivity check failed: %s", resp.Status)
	}
	return nil
}

func (p *Provider) baseURL() string {
	if p.cfg.BaseURL != "" {
		return p.cfg.BaseURL
	}
	return "https://api.openai.com/v1"
}

func (p *Provider) isLocalEndpoint() bool {
	baseURL := strings.TrimSpace(strings.ToLower(p.baseURL()))
	return strings.Contains(baseURL, "127.0.0.1") ||
		strings.Contains(baseURL, "localhost") ||
		strings.Contains(baseURL, "0.0.0.0")
}

func (p *Provider) usesNativeLMStudioChat() bool {
	return p.isLocalEndpoint() && len(p.cfg.Integrations) > 0
}

func (p *Provider) chatURL() string {
	if p.usesNativeLMStudioChat() {
		return strings.TrimRight(p.nativeLMStudioBaseURL(), "/") + "/chat"
	}
	return strings.TrimRight(p.baseURL(), "/") + "/chat/completions"
}

func (p *Provider) nativeLMStudioBaseURL() string {
	baseURL := strings.TrimRight(p.baseURL(), "/")
	if strings.HasSuffix(baseURL, "/api/v1") {
		return baseURL
	}
	if strings.HasSuffix(baseURL, "/v1") {
		return strings.TrimSuffix(baseURL, "/v1") + "/api/v1"
	}
	return baseURL + "/api/v1"
}

func (p *Provider) chatRequestBody(messages []types.Message) map[string]any {
	if p.usesNativeLMStudioChat() {
		// LM Studio's native MCP-aware endpoint accepts a single transcript string
		// plus integrations, not OpenAI-style role/content messages.
		transcriptMessages := p.withMCPSystemPrompt(messages)
		return map[string]any{
			"model":        p.cfg.Model,
			"input":        toLMStudioInput(transcriptMessages),
			"integrations": append([]string(nil), p.cfg.Integrations...),
			"store":        false,
		}
	}
	body := map[string]any{
		"model":    p.cfg.Model,
		"messages": toChatMessages(messages),
	}
	return body
}

func (p *Provider) formatAPIError(statusCode int, status, body string) error {
	if p.usesNativeLMStudioChat() {
		if err := p.formatLMStudioError(statusCode, body); err != nil {
			return err
		}
	}
	return fmt.Errorf("openai request failed: %s: %s", status, body)
}

func (p *Provider) formatLMStudioError(statusCode int, body string) error {
	var decoded struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Param   string `json:"param"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return nil
	}

	message := strings.TrimSpace(decoded.Error.Message)
	if message == "" {
		return nil
	}

	if statusCode == http.StatusForbidden && decoded.Error.Param == "integrations" {
		integration := firstIntegrationName(p.cfg.Integrations)
		if integration == "" {
			integration = "configured MCP integration"
		}
		return fmt.Errorf("LM Studio denied MCP server %s. Check LM Studio MCP/plugin permissions and server settings.", integration)
	}
	if decoded.Error.Param == "integrations" {
		return fmt.Errorf("LM Studio rejected the configured MCP integrations: %s", message)
	}
	return fmt.Errorf("LM Studio request failed: %s", message)
}

func (p *Provider) withMCPSystemPrompt(messages []types.Message) []types.Message {
	if !p.usesNativeLMStudioChat() {
		return messages
	}
	prompt := strings.TrimSpace(p.mcpSystemPrompt())
	if prompt == "" {
		return messages
	}

	out := make([]types.Message, 0, len(messages)+1)
	out = append(out, types.Message{
		Role: types.RoleSystem,
		Text: prompt,
	})
	out = append(out, messages...)
	return out
}

func (p *Provider) mcpSystemPrompt() string {
	if len(p.cfg.Integrations) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"You have access to these MCP integrations: %s. When a request needs browser access, website interaction, or live external information that these integrations can provide, use them instead of saying you cannot browse or access real-time content. If an integration call fails or is denied, explain that clearly and continue with the best available help.",
		strings.Join(p.cfg.Integrations, ", "),
	)
}

func toChatMessages(messages []types.Message) []map[string]string {
	out := make([]map[string]string, 0, len(messages))
	for _, msg := range messages {
		role := string(msg.Role)
		if role == "" {
			role = string(types.RoleUser)
		}
		out = append(out, map[string]string{
			"role":    role,
			"content": msg.Text,
		})
	}
	return out
}

func toLMStudioInput(messages []types.Message) string {
	// Preserve the role-annotated transcript because LM Studio's native endpoint
	// expects one input string rather than structured chat messages.
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		role := string(msg.Role)
		if role == "" {
			role = string(types.RoleUser)
		}
		label := "User"
		switch types.Role(role) {
		case types.RoleSystem:
			label = "System"
		case types.RoleAssistant:
			label = "Assistant"
		}
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", label, text))
	}
	return strings.Join(parts, "\n\n")
}

func firstIntegrationName(integrations []string) string {
	for _, name := range integrations {
		name = strings.TrimSpace(name)
		if name != "" {
			return name
		}
	}
	return ""
}

func endpointClass(baseURL string) string {
	baseURL = strings.TrimSpace(strings.ToLower(baseURL))
	if baseURL == "" {
		return "remote"
	}
	switch {
	case strings.Contains(baseURL, "127.0.0.1"),
		strings.Contains(baseURL, "localhost"),
		strings.Contains(baseURL, "0.0.0.0"):
		return "local"
	default:
		return "remote"
	}
}
