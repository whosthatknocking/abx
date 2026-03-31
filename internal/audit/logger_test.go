package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/whosthatknocking/abx/internal/config"
)

func TestLoggerRedactsTruncatesAndRecordsExitStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, err := New(config.AuditConfig{
		FilePath:       path,
		RetentionDays:  30,
		MaxOutputBytes: 12,
	})
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	defer logger.Close()

	status := 7
	if err := logger.Log(Record{
		Time:         time.Now(),
		Event:        "command_executed",
		Command:      "echo sk-secret",
		ApprovalText: "YES bearer abc123",
		Output:       "0123456789abcdef",
		Error:        "bearer token leaked",
		ExitStatus:   &status,
	}); err != nil {
		t.Fatalf("log record: %v", err)
	}

	records := readRecords(t, path)
	if len(records) != 1 {
		t.Fatalf("expected one record, got %d", len(records))
	}
	got := records[0]
	if !strings.Contains(got.Command, "[REDACTED]") {
		t.Fatalf("expected redacted command, got %q", got.Command)
	}
	if !strings.Contains(got.ApprovalText, "[REDACTED]") {
		t.Fatalf("expected redacted approval text, got %q", got.ApprovalText)
	}
	if !strings.Contains(got.Error, "[REDACTED]") {
		t.Fatalf("expected redacted error, got %q", got.Error)
	}
	if !got.Truncated {
		t.Fatal("expected output truncation")
	}
	if len(got.Output) != 12 {
		t.Fatalf("expected truncated output length 12, got %d", len(got.Output))
	}
	if got.ExitStatus == nil || *got.ExitStatus != 7 {
		t.Fatalf("unexpected exit status: %#v", got.ExitStatus)
	}
}

func TestLoggerPrunesExpiredRecordsOnStartup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	writeAuditLine(t, path, Record{
		Time:  time.Now().AddDate(0, 0, -31),
		Event: "old",
	})
	writeAuditLine(t, path, Record{
		Time:  time.Now().AddDate(0, 0, -5),
		Event: "recent",
	})

	logger, err := New(config.AuditConfig{
		FilePath:       path,
		RetentionDays:  30,
		MaxOutputBytes: 1024,
	})
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	defer logger.Close()

	records := readRecords(t, path)
	if len(records) != 1 {
		t.Fatalf("expected one retained record, got %d", len(records))
	}
	if records[0].Event != "recent" {
		t.Fatalf("unexpected retained record: %#v", records[0])
	}
}

func readRecords(t *testing.T, path string) []Record {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	records := make([]Record, 0, len(lines))
	for _, line := range lines {
		var record Record
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("unmarshal audit record: %v", err)
		}
		records = append(records, record)
	}
	return records
}

func writeAuditLine(t *testing.T, path string, record Record) {
	t.Helper()

	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal audit record: %v", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open audit file: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write audit record: %v", err)
	}
}
