package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/whosthatknocking/abx/pkg/types"
)

type stubProvider struct {
	chatResponse types.AgentResponse
	chatErr      error
	checkErr     error
	chatCalls    int
	checkCalls   int
}

func (p *stubProvider) Chat(_ context.Context, _ []types.Message, _ []types.Tool) (types.AgentResponse, error) {
	p.chatCalls++
	return p.chatResponse, p.chatErr
}

func (p *stubProvider) Check(_ context.Context) error {
	p.checkCalls++
	return p.checkErr
}

func TestFallbackProviderUsesPrimaryWhenSuccessful(t *testing.T) {
	primary := &stubProvider{chatResponse: types.AgentResponse{Text: "primary"}}
	fallback := &stubProvider{chatResponse: types.AgentResponse{Text: "fallback"}}

	provider := NewFallback(primary, fallback)
	resp, err := provider.Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.Text != "primary" {
		t.Fatalf("unexpected response %q", resp.Text)
	}
	if primary.chatCalls != 1 || fallback.chatCalls != 0 {
		t.Fatalf("unexpected call counts: primary=%d fallback=%d", primary.chatCalls, fallback.chatCalls)
	}
}

func TestFallbackProviderUsesFallbackWhenPrimaryFails(t *testing.T) {
	primary := &stubProvider{chatErr: errors.New("primary failed")}
	fallback := &stubProvider{chatResponse: types.AgentResponse{Text: "fallback"}}

	provider := NewFallback(primary, fallback)
	resp, err := provider.Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.Text != "fallback" {
		t.Fatalf("unexpected response %q", resp.Text)
	}
	if primary.chatCalls != 1 || fallback.chatCalls != 1 {
		t.Fatalf("unexpected call counts: primary=%d fallback=%d", primary.chatCalls, fallback.chatCalls)
	}
}

func TestFallbackProviderCheckFallsBack(t *testing.T) {
	primary := &stubProvider{checkErr: errors.New("primary failed")}
	fallback := &stubProvider{}

	provider := NewFallback(primary, fallback)
	if err := provider.Check(context.Background()); err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if primary.checkCalls != 1 || fallback.checkCalls != 1 {
		t.Fatalf("unexpected check call counts: primary=%d fallback=%d", primary.checkCalls, fallback.checkCalls)
	}
}
