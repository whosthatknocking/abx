package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/whosthatknocking/abx/internal/config"
)

type Logger struct {
	mu        sync.Mutex
	file      *os.File
	maxOutput int
}

type Record struct {
	Time           time.Time `json:"time"`
	Event          string    `json:"event"`
	ConversationID string    `json:"conversation_id,omitempty"`
	SessionID      string    `json:"session_id,omitempty"`
	Sender         string    `json:"sender,omitempty"`
	Recipient      string    `json:"recipient,omitempty"`
	RequestID      string    `json:"request_id,omitempty"`
	ApprovedBy     string    `json:"approved_by,omitempty"`
	MessageType    string    `json:"message_type,omitempty"`
	Command        string    `json:"command,omitempty"`
	Decision       string    `json:"decision,omitempty"`
	Output         string    `json:"output,omitempty"`
	Error          string    `json:"error,omitempty"`
	Truncated      bool      `json:"truncated,omitempty"`
}

func New(cfg config.AuditConfig) (*Logger, error) {
	if cfg.FilePath == "" {
		return nil, fmt.Errorf("audit.file_path is required")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.FilePath), 0o755); err != nil {
		return nil, fmt.Errorf("create audit dir: %w", err)
	}
	f, err := os.OpenFile(cfg.FilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open audit file: %w", err)
	}
	return &Logger{
		file:      f,
		maxOutput: cfg.MaxOutputBytes,
	}, nil
}

func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

func (l *Logger) Log(record Record) error {
	if l == nil || l.file == nil {
		return nil
	}
	if record.Time.IsZero() {
		record.Time = time.Now()
	}
	record.Command = redact(record.Command)
	record.Output, record.Truncated = sanitize(record.Output, l.maxOutput)
	record.Error, _ = sanitize(record.Error, l.maxOutput)

	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.file.Write(append(raw, '\n'))
	return err
}

func sanitize(value string, max int) (string, bool) {
	value = redact(value)
	if max <= 0 || len(value) <= max {
		return value, false
	}
	return value[:max], true
}

var redactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9_-]+`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._-]+`),
}

func redact(value string) string {
	if value == "" {
		return value
	}
	out := value
	for _, pattern := range redactionPatterns {
		out = pattern.ReplaceAllString(out, "[REDACTED]")
	}
	out = strings.ReplaceAll(out, "\x00", "")
	return out
}
