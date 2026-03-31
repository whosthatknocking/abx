package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/whosthatknocking/abx/internal/config"
)

type Logger struct {
	mu            sync.Mutex
	file          *os.File
	path          string
	maxOutput     int
	retentionDays int
	lastPruneDay  string
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
	ApprovalText   string    `json:"approval_text,omitempty"`
	MessageType    string    `json:"message_type,omitempty"`
	Command        string    `json:"command,omitempty"`
	Decision       string    `json:"decision,omitempty"`
	Output         string    `json:"output,omitempty"`
	Error          string    `json:"error,omitempty"`
	Truncated      bool      `json:"truncated,omitempty"`
	ExitStatus     *int      `json:"exit_status,omitempty"`
}

func New(cfg config.AuditConfig) (*Logger, error) {
	if cfg.FilePath == "" {
		return nil, fmt.Errorf("audit.file_path is required")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.FilePath), 0o755); err != nil {
		return nil, fmt.Errorf("create audit dir: %w", err)
	}
	if err := pruneFile(cfg.FilePath, cfg.RetentionDays); err != nil {
		return nil, fmt.Errorf("prune audit file: %w", err)
	}
	f, err := os.OpenFile(cfg.FilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open audit file: %w", err)
	}
	return &Logger{
		file:          f,
		path:          cfg.FilePath,
		maxOutput:     cfg.MaxOutputBytes,
		retentionDays: cfg.RetentionDays,
		lastPruneDay:  time.Now().Format("2006-01-02"),
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
	record.ApprovalText = redact(record.ApprovalText)
	record.Output, record.Truncated = sanitize(record.Output, l.maxOutput)
	record.Error, _ = sanitize(record.Error, l.maxOutput)

	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.pruneIfNeededLocked(record.Time); err != nil {
		return err
	}
	_, err = l.file.Write(append(raw, '\n'))
	return err
}

func (l *Logger) pruneIfNeededLocked(now time.Time) error {
	if l.retentionDays <= 0 {
		return nil
	}
	day := now.Format("2006-01-02")
	if day == l.lastPruneDay {
		return nil
	}
	if err := l.file.Close(); err != nil {
		return err
	}
	if err := pruneFile(l.path, l.retentionDays); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	l.file = f
	l.lastPruneDay = day
	return nil
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

func pruneFile(path string, retentionDays int) error {
	if retentionDays <= 0 {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() == 0 {
		return nil
	}

	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	tmpPath := path + ".tmp"
	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	reader := json.NewDecoder(src)
	keepAny := false
	for {
		var record Record
		if err := reader.Decode(&record); err != nil {
			if err == io.EOF {
				break
			}
			_ = dst.Close()
			_ = os.Remove(tmpPath)
			return err
		}
		if !record.Time.IsZero() && record.Time.Before(cutoff) {
			continue
		}
		raw, err := json.Marshal(record)
		if err != nil {
			_ = dst.Close()
			_ = os.Remove(tmpPath)
			return err
		}
		if _, err := dst.Write(append(raw, '\n')); err != nil {
			_ = dst.Close()
			_ = os.Remove(tmpPath)
			return err
		}
		keepAny = true
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if !keepAny {
		if err := os.WriteFile(tmpPath, nil, 0o644); err != nil {
			_ = os.Remove(tmpPath)
			return err
		}
	}
	return os.Rename(tmpPath, path)
}
