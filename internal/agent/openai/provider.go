package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
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
	timeout := time.Duration(cfg.RequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Provider{
		cfg: cfg,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (p *Provider) Chat(ctx context.Context, messages []types.Message, _ []types.Tool) (types.AgentResponse, error) {
	return p.ChatWithOptions(ctx, messages, nil, types.AgentOptions{})
}

func (p *Provider) ChatWithOptions(ctx context.Context, messages []types.Message, _ []types.Tool, options types.AgentOptions) (types.AgentResponse, error) {
	body := p.chatRequestBody(messages, options)
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
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
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
		InputTokens:   decoded.Usage.PromptTokens,
		OutputTokens:  decoded.Usage.CompletionTokens,
		TotalTokens:   decoded.Usage.TotalTokens,
	}, nil
}

func (p *Provider) decodeLMStudioChatResponse(body io.Reader) (types.AgentResponse, error) {
	var decoded struct {
		Output []struct {
			Type     string `json:"type"`
			Content  string `json:"content"`
			Output   string `json:"output"`
			Reason   string `json:"reason"`
			Tool     string `json:"tool"`
			Provider struct {
				Type        string `json:"type"`
				PluginID    string `json:"plugin_id"`
				ServerLabel string `json:"server_label"`
			} `json:"provider_info"`
		} `json:"output"`
		Stats struct {
			InputTokens             int     `json:"input_tokens"`
			TotalOutputTokens       int     `json:"total_output_tokens"`
			TimeToFirstTokenSeconds float64 `json:"time_to_first_token_seconds"`
		} `json:"stats"`
	}
	if err := json.NewDecoder(body).Decode(&decoded); err != nil {
		return types.AgentResponse{}, fmt.Errorf("decode LM Studio response: %w", err)
	}

	parts := make([]string, 0, len(decoded.Output))
	integrations := make([]string, 0, len(decoded.Output))
	toolSummaries := make([]string, 0, len(decoded.Output))
	seenIntegrations := make(map[string]struct{}, len(decoded.Output))
	for _, item := range decoded.Output {
		if item.Type == "message" {
			text := strings.TrimSpace(item.Content)
			if text != "" {
				parts = append(parts, text)
			}
		}
		if item.Type == "tool_call" {
			name := strings.TrimSpace(item.Provider.PluginID)
			if name == "" {
				name = strings.TrimSpace(item.Provider.ServerLabel)
			}
			if name != "" {
				if _, exists := seenIntegrations[name]; !exists {
					seenIntegrations[name] = struct{}{}
					integrations = append(integrations, name)
				}
			}
			if summary := summarizeToolCall(item.Tool, item.Output); summary != "" {
				toolSummaries = append(toolSummaries, summary)
			}
		}
	}
	text := strings.Join(parts, "\n\n")
	if shouldAppendToolSummaries(text) && len(toolSummaries) > 0 {
		text = appendToolSummaries(text, toolSummaries)
	}
	if len(parts) > 0 {
		return types.AgentResponse{
			Text:          text,
			Provider:      strings.TrimSpace(p.cfg.Provider),
			Model:         strings.TrimSpace(p.cfg.Model),
			EndpointClass: endpointClass(p.cfg.BaseURL),
			Integrations:  integrations,
			ToolSummaries: append([]string(nil), toolSummaries...),
			InputTokens:   decoded.Stats.InputTokens,
			OutputTokens:  decoded.Stats.TotalOutputTokens,
			TotalTokens:   decoded.Stats.InputTokens + decoded.Stats.TotalOutputTokens,
			TimeToFirst:   decoded.Stats.TimeToFirstTokenSeconds,
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

func (p *Provider) chatRequestBody(messages []types.Message, options types.AgentOptions) map[string]any {
	messages = p.applyThinkingMessages(messages, options)
	if p.usesNativeLMStudioChat() {
		// LM Studio's native MCP-aware endpoint accepts a single transcript string
		// plus integrations, with optional image items for user attachments.
		transcriptMessages := p.applyThinkingSystemMessages(p.withMCPSystemPrompt(messages), options)
		body := map[string]any{
			"model":        p.cfg.Model,
			"input":        p.toLMStudioInputItems(transcriptMessages),
			"integrations": append([]string(nil), p.cfg.Integrations...),
			"store":        false,
		}
		p.applyNativeThinkingRequestBody(body, options)
		return body
	}
	messages = p.applyThinkingSystemMessages(messages, options)
	body := map[string]any{
		"model":    p.cfg.Model,
		"messages": p.toChatMessages(messages),
	}
	p.applyThinkingRequestBody(body, options)
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

func (p *Provider) toChatMessages(messages []types.Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		role := string(msg.Role)
		if role == "" {
			role = string(types.RoleUser)
		}
		out = append(out, map[string]any{
			"role":    role,
			"content": p.chatMessageContent(msg),
		})
	}
	return out
}

func (p *Provider) chatMessageContent(msg types.Message) any {
	if len(msg.Attachments) == 0 {
		return msg.Text
	}
	parts := make([]map[string]any, 0, 1+len(msg.Attachments))
	if text := strings.TrimSpace(msg.Text); text != "" {
		parts = append(parts, map[string]any{
			"type": "text",
			"text": text,
		})
	}
	for _, attachment := range msg.Attachments {
		dataURL, ok := attachmentDataURL(attachment)
		if !ok {
			continue
		}
		parts = append(parts, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": dataURL,
			},
		})
	}
	if len(parts) == 0 {
		return msg.Text
	}
	return parts
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
		if attachmentText := lmStudioAttachmentText(msg.Attachments); attachmentText != "" {
			if text == "" {
				text = attachmentText
			} else {
				text += "\n" + attachmentText
			}
		}
		if text == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", label, text))
	}
	return strings.Join(parts, "\n\n")
}

func (p *Provider) toLMStudioInputItems(messages []types.Message) any {
	imageAttachments := p.latestRelevantUserImageAttachments(messages)
	input := toLMStudioInput(messages)
	if len(imageAttachments) == 0 {
		return input
	}
	items := make([]map[string]any, 0, 1+len(imageAttachments))
	if strings.TrimSpace(input) != "" {
		items = append(items, map[string]any{
			"type":    "text",
			"content": input,
		})
	}
	for _, attachment := range imageAttachments {
		dataURL, ok := attachmentDataURL(attachment)
		if !ok {
			continue
		}
		items = append(items, map[string]any{
			"type":     "image",
			"data_url": dataURL,
		})
	}
	if len(items) == 0 {
		return input
	}
	return items
}

func (p *Provider) latestRelevantUserImageAttachments(messages []types.Message) []types.Attachment {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != types.RoleUser {
			continue
		}
		out := make([]types.Attachment, 0, len(messages[i].Attachments))
		for _, attachment := range messages[i].Attachments {
			if strings.EqualFold(strings.TrimSpace(attachment.Kind), "image") {
				out = append(out, attachment)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func lmStudioAttachmentText(attachments []types.Attachment) string {
	count := 0
	for _, attachment := range attachments {
		if strings.EqualFold(strings.TrimSpace(attachment.Kind), "image") {
			count++
		}
	}
	if count == 0 {
		return ""
	}
	if count == 1 {
		return "[Attached image]"
	}
	return fmt.Sprintf("[Attached %d images]", count)
}

func attachmentDataURL(attachment types.Attachment) (string, bool) {
	contentType := strings.TrimSpace(attachment.ContentType)
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return "", false
	}
	filePath := strings.TrimSpace(attachment.FilePath)
	if filePath == "" {
		return "", false
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return "", false
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(raw), true
}

func (p *Provider) applyThinkingSystemMessages(messages []types.Message, options types.AgentOptions) []types.Message {
	prompt := p.thinkingSystemPrompt(options)
	if prompt == "" {
		return append([]types.Message(nil), messages...)
	}
	out := append([]types.Message(nil), messages...)
	for i := range out {
		if out[i].Role != types.RoleSystem {
			continue
		}
		text := strings.TrimSpace(out[i].Text)
		if text == "" {
			out[i].Text = prompt
		} else if !strings.HasPrefix(text, prompt) {
			out[i].Text = prompt + "\n\n" + text
		}
		return out
	}
	return append([]types.Message{{Role: types.RoleSystem, Text: prompt}}, out...)
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

func (p *Provider) applyThinkingMessages(messages []types.Message, options types.AgentOptions) []types.Message {
	suffix := p.thinkingSuffix(options)
	if suffix == "" {
		return append([]types.Message(nil), messages...)
	}
	out := append([]types.Message(nil), messages...)
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role != types.RoleUser {
			continue
		}
		text := strings.TrimSpace(out[i].Text)
		if text == "" {
			text = suffix
		} else if !strings.HasSuffix(text, suffix) {
			text += "\n" + suffix
		}
		out[i].Text = text
		return out
	}
	out = append(out, types.Message{Role: types.RoleUser, Text: suffix})
	return out
}

func (p *Provider) applyThinkingRequestBody(body map[string]any, options types.AgentOptions) {
	path := strings.TrimSpace(p.cfg.Thinking.ParameterPath)
	if path == "" {
		return
	}
	value, ok := p.thinkingRequestBodyValue(options)
	if !ok {
		return
	}
	setNestedValue(body, strings.Split(path, "."), value)
}

func (p *Provider) applyNativeThinkingRequestBody(body map[string]any, options types.AgentOptions) {
	if strings.TrimSpace(p.cfg.Thinking.ParameterPath) != "reasoning" {
		return
	}
	value, ok := p.thinkingRequestBodyValue(options)
	if !ok {
		return
	}
	body["reasoning"] = value
}

func (p *Provider) thinkingSuffix(options types.AgentOptions) string {
	value, ok := p.thinkingValue(options)
	if !ok {
		return ""
	}
	if value {
		return strings.TrimSpace(p.cfg.Thinking.EnableSuffix)
	}
	return strings.TrimSpace(p.cfg.Thinking.DisableSuffix)
}

func (p *Provider) thinkingSystemPrompt(options types.AgentOptions) string {
	value, ok := p.thinkingValue(options)
	if !ok {
		return ""
	}
	if value {
		return strings.TrimSpace(p.cfg.Thinking.EnableSystemPrompt)
	}
	return strings.TrimSpace(p.cfg.Thinking.DisableSystemPrompt)
}

func (p *Provider) thinkingRequestBodyValue(options types.AgentOptions) (any, bool) {
	value, ok := p.thinkingValue(options)
	if !ok {
		return nil, false
	}
	if value {
		if configured := strings.TrimSpace(p.cfg.Thinking.EnableParameterValue); configured != "" {
			return configured, true
		}
	} else if configured := strings.TrimSpace(p.cfg.Thinking.DisableParameterValue); configured != "" {
		return configured, true
	}
	return value, true
}

func (p *Provider) thinkingValue(options types.AgentOptions) (bool, bool) {
	if options.Thinking != nil {
		return *options.Thinking, true
	}
	switch p.cfg.Thinking.DefaultMode {
	case "enabled":
		return true, true
	case "disabled":
		return false, true
	default:
		return false, false
	}
}

func setNestedValue(root map[string]any, path []string, value any) {
	if len(path) == 0 {
		return
	}
	current := root
	for _, part := range path[:len(path)-1] {
		part = strings.TrimSpace(part)
		if part == "" {
			return
		}
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	last := strings.TrimSpace(path[len(path)-1])
	if last == "" {
		return
	}
	current[last] = value
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

func shouldAppendToolSummaries(text string) bool {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return true
	}
	for _, marker := range []string{
		"provided above",
		"shown above",
		"listed above",
		"see above",
		"above.",
		"above,",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func appendToolSummaries(text string, summaries []string) string {
	lines := make([]string, 0, len(summaries)+1)
	lines = append(lines, "Tool results:")
	for _, summary := range summaries {
		summary = strings.TrimSpace(summary)
		if summary == "" {
			continue
		}
		lines = append(lines, "- "+summary)
	}
	suffix := strings.Join(lines, "\n")
	if strings.TrimSpace(text) == "" {
		return suffix
	}
	return strings.TrimSpace(text) + "\n\n" + suffix
}

func summarizeToolCall(tool, output string) string {
	texts := extractToolOutputTexts(output)
	if len(texts) == 0 {
		return ""
	}
	switch strings.TrimSpace(tool) {
	case "get_option_expirations":
		return summarizeOptionExpirations(texts)
	case "get_option_chain":
		if summary := summarizeOptionChain(texts[0]); summary != "" {
			return summary
		}
	}
	return summarizeGenericToolOutput(tool, texts)
}

func extractToolOutputTexts(output string) []string {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(output), &items); err == nil {
		out := make([]string, 0, len(items))
		for _, item := range items {
			if strings.TrimSpace(item.Text) != "" {
				out = append(out, strings.TrimSpace(item.Text))
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{output}
}

func summarizeOptionExpirations(texts []string) string {
	values := make([]string, 0, len(texts))
	for _, text := range texts {
		text = strings.TrimSpace(text)
		if text != "" {
			values = append(values, text)
		}
	}
	if len(values) == 0 {
		return ""
	}
	preview := values
	if len(preview) > 6 {
		preview = preview[:6]
	}
	summary := fmt.Sprintf("Option expirations: %s", strings.Join(preview, ", "))
	if len(values) > len(preview) {
		summary += fmt.Sprintf(" (%d total)", len(values))
	}
	return summary
}

func summarizeOptionChain(text string) string {
	var decoded struct {
		Symbol string `json:"symbol"`
		Date   string `json:"date"`
		Calls  struct {
			Data [][]any `json:"data"`
		} `json:"calls"`
		Puts struct {
			Data [][]any `json:"data"`
		} `json:"puts"`
	}
	if err := json.Unmarshal([]byte(text), &decoded); err == nil {
		if decoded.Symbol != "" || decoded.Date != "" {
			return fmt.Sprintf(
				"Option chain for %s on %s: %d calls, %d puts.",
				strings.TrimSpace(decoded.Symbol),
				strings.TrimSpace(decoded.Date),
				len(decoded.Calls.Data),
				len(decoded.Puts.Data),
			)
		}
	}
	return ""
}

func summarizeGenericToolOutput(tool string, texts []string) string {
	joined := strings.TrimSpace(strings.Join(texts, " "))
	if joined == "" {
		return ""
	}
	joined = compactToolWhitespace(joined)
	if len(joined) > 240 {
		joined = joined[:237] + "..."
	}
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return joined
	}
	return fmt.Sprintf("%s: %s", tool, joined)
}

var toolWhitespace = regexp.MustCompile(`\s+`)

func compactToolWhitespace(text string) string {
	return strings.TrimSpace(toolWhitespace.ReplaceAllString(text, " "))
}
