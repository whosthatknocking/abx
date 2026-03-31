package signalcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/whosthatknocking/abx/pkg/types"
)

func TestSendParamsDirectConversation(t *testing.T) {
	params, err := sendParams("+15550001111", "direct:+15551234567", "hello")
	if err != nil {
		t.Fatalf("sendParams returned error: %v", err)
	}

	want := map[string]any{
		"account":   "+15550001111",
		"message":   "hello",
		"recipient": []string{"+15551234567"},
	}
	if !reflect.DeepEqual(params, want) {
		t.Fatalf("unexpected direct send params: got %#v want %#v", params, want)
	}
}

func TestSendParamsGroupConversation(t *testing.T) {
	params, err := sendParams("+15550001111", "group:test-group-id", "hello")
	if err != nil {
		t.Fatalf("sendParams returned error: %v", err)
	}

	want := map[string]any{
		"account": "+15550001111",
		"message": "hello",
		"groupId": "test-group-id",
	}
	if !reflect.DeepEqual(params, want) {
		t.Fatalf("unexpected group send params: got %#v want %#v", params, want)
	}
}

func TestSendParamsRejectsEmptyConversationTarget(t *testing.T) {
	if _, err := sendParams("+15550001111", "direct:", "hello"); err == nil {
		t.Fatal("expected error for empty direct recipient")
	}
	if _, err := sendParams("+15550001111", "group:", "hello"); err == nil {
		t.Fatal("expected error for empty group recipient")
	}
}

func TestAwaitRPCResponseReturnsMatchingResult(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		_, _ = server.Write([]byte(`{"jsonrpc":"2.0","method":"receive","params":{"envelope":{"sourceNumber":"+15551230000","timestamp":1710000000000,"dataMessage":{"message":"ignore"}}}}` + "\n"))
		_, _ = server.Write([]byte(`{"jsonrpc":"2.0","id":"target","result":{"timestamp":1710000000001}}` + "\n"))
	}()

	got, err := awaitRPCResponse(context.Background(), client, bufio.NewReader(client), "target")
	if err != nil {
		t.Fatalf("awaitRPCResponse returned error: %v", err)
	}

	if got["timestamp"] != float64(1710000000001) {
		t.Fatalf("unexpected rpc result: %#v", got)
	}
}

func TestAwaitRPCResponseReturnsRPCError(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		_, _ = server.Write([]byte(`{"jsonrpc":"2.0","id":"target","error":{"code":-32602,"message":"bad params"}}` + "\n"))
	}()

	_, err := awaitRPCResponse(context.Background(), client, bufio.NewReader(client), "target")
	if err == nil {
		t.Fatal("expected rpc error")
	}
	if got := err.Error(); got != "signal-cli rpc error -32602: bad params" {
		t.Fatalf("unexpected rpc error: %s", got)
	}
}

func TestIsMethodNotImplementedError(t *testing.T) {
	if !isMethodNotImplementedError(errors.New("signal-cli rpc error -32601: Method not implemented")) {
		t.Fatal("expected method-not-implemented error to be detected")
	}
	if isMethodNotImplementedError(errors.New("signal-cli rpc error -32602: bad params")) {
		t.Fatal("did not expect bad-params error to be treated as method-not-implemented")
	}
}

func TestDecodeEnvelopeFromRPCDirect(t *testing.T) {
	payload := mustRPCMessage(t, map[string]any{
		"jsonrpc": "2.0",
		"method":  "receive",
		"params": map[string]any{
			"envelope": map[string]any{
				"sourceNumber": "+15551230000",
				"timestamp":    1710000000000,
				"dataMessage": map[string]any{
					"message": " hello ",
				},
			},
		},
	})

	got, ok := decodeEnvelopeFromRPC("+15550001111", payload)
	if !ok {
		t.Fatal("expected envelope to decode")
	}

	wantTime := time.UnixMilli(1710000000000)
	if got.ConversationID != "direct:+15551230000" ||
		got.Sender != "+15551230000" ||
		got.Recipient != "+15550001111" ||
		got.ChatType != types.ChatTypeDirect ||
		got.Text != "hello" ||
		!got.CreatedAt.Equal(wantTime) {
		t.Fatalf("unexpected decoded envelope: %#v", got)
	}
}

func TestDecodeEnvelopeFromRPCGroupMention(t *testing.T) {
	payload := mustRPCMessage(t, map[string]any{
		"jsonrpc": "2.0",
		"method":  "receive",
		"params": map[string]any{
			"envelope": map[string]any{
				"sourceNumber": "+15551230000",
				"timestamp":    "1710000000000",
				"dataMessage": map[string]any{
					"message": "status?",
					"groupInfo": map[string]any{
						"groupId": "group-123",
					},
					"mentions": []any{
						map[string]any{"number": "+15550001111"},
					},
				},
			},
		},
	})

	got, ok := decodeEnvelopeFromRPC("+15550001111", payload)
	if !ok {
		t.Fatal("expected group envelope to decode")
	}
	if got.ConversationID != "group:group-123" {
		t.Fatalf("unexpected conversation id: %s", got.ConversationID)
	}
	if got.ChatType != types.ChatTypeGroup {
		t.Fatalf("unexpected chat type: %s", got.ChatType)
	}
	if !got.MentionedBot {
		t.Fatal("expected mention to be detected")
	}
}

func mustRPCMessage(t *testing.T, payload map[string]any) rpcMessage {
	t.Helper()

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal rpc payload: %v", err)
	}

	var msg rpcMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal rpc payload: %v", err)
	}
	return msg
}
