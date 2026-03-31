package signalcli

import (
	"reflect"
	"testing"
)

func TestSendParamsDirectConversation(t *testing.T) {
	params, err := sendParams("+15550001111", "direct:+15551234567", "hello")
	if err != nil {
		t.Fatalf("sendParams returned error: %v", err)
	}

	want := map[string]any{
		"account":    "+15550001111",
		"message":    "hello",
		"recipients": []string{"+15551234567"},
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
