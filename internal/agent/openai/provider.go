package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	logger *log.Logger
}

var _ agent.Provider = (*Provider)(nil)

func New(cfg config.ProviderConfig, logger *log.Logger) *Provider {
	return &Provider{
		cfg: cfg,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
		logger: logger,
	}
}

func (p *Provider) Chat(ctx context.Context, messages []types.Message, _ []types.Tool) (types.AgentResponse, error) {
	body := map[string]any{
		"model":    p.cfg.Model,
		"messages": toChatMessages(messages),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return types.AgentResponse{}, err
	}

	url := strings.TrimRight(p.baseURL(), "/") + "/chat/completions"
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
		return types.AgentResponse{}, fmt.Errorf("openai request failed: %s", resp.Status)
	}

	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return types.AgentResponse{}, fmt.Errorf("decode openai response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return types.AgentResponse{}, fmt.Errorf("openai response did not contain any choices")
	}
	return types.AgentResponse{Text: strings.TrimSpace(decoded.Choices[0].Message.Content)}, nil
}

func (p *Provider) Check(ctx context.Context) error {
	url := strings.TrimRight(p.baseURL(), "/") + "/models/" + p.cfg.Model
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
