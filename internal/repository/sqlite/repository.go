package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/whosthatknocking/abx/internal/repository"
	"github.com/whosthatknocking/abx/pkg/types"
)

type Repository struct {
	dsn string
}

func New(dsn string) (repository.Repository, error) {
	if dsn == "" {
		return nil, fmt.Errorf("database dsn is required")
	}
	if err := os.MkdirAll(filepath.Dir(dsn), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite dir: %w", err)
	}
	r := &Repository{dsn: dsn}
	if err := r.init(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Repository) init() error {
	schema := `
CREATE TABLE IF NOT EXISTS conversations (
  conversation_id TEXT PRIMARY KEY,
  active_session_id TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  conversation_id TEXT NOT NULL,
  session_id TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  persona TEXT NOT NULL DEFAULT '',
  format TEXT NOT NULL DEFAULT '',
  fallback_disabled INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (conversation_id, session_id)
);
CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL,
  session_id TEXT NOT NULL,
  sender TEXT NOT NULL,
  recipient TEXT NOT NULL,
  role TEXT NOT NULL,
  kind TEXT NOT NULL,
  chat_type TEXT NOT NULL,
  text TEXT NOT NULL,
  mentioned_bot INTEGER NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS pending_approvals (
  request_id TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL,
  session_id TEXT NOT NULL,
  command_text TEXT NOT NULL,
  proposed_by TEXT NOT NULL,
  nonce TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  is_active INTEGER NOT NULL
);
`
	_, err := r.exec(schema)
	if err != nil {
		return err
	}
	if err := r.ensureSessionPersonaColumn(); err != nil {
		return err
	}
	if err := r.ensureSessionFormatColumn(); err != nil {
		return err
	}
	return r.ensureSessionFallbackDisabledColumn()
}

func (r *Repository) SaveMessage(_ context.Context, conversationID, sessionID string, msg types.Message) error {
	if err := r.ensureConversation(conversationID); err != nil {
		return err
	}
	if sessionID == "" {
		var err error
		sessionID, err = r.GetActiveSessionID(context.Background(), conversationID)
		if err != nil {
			return err
		}
	}
	if err := r.ensureSession(conversationID, sessionID); err != nil {
		return err
	}
	sql := fmt.Sprintf(`INSERT OR REPLACE INTO messages
		(id, conversation_id, session_id, sender, recipient, role, kind, chat_type, text, mentioned_bot, created_at)
		VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%d,%s);`,
		q(msg.ID), q(conversationID), q(sessionID), q(msg.Sender), q(msg.Recipient), q(string(msg.Role)),
		q(string(msg.Kind)), q(string(msg.ChatType)), q(msg.Text), boolInt(msg.MentionedBot), q(msg.CreatedAt.Format(timeLayout)))
	_, err := r.exec(sql)
	return err
}

func (r *Repository) GetActiveSessionID(_ context.Context, conversationID string) (string, error) {
	if err := r.ensureConversation(conversationID); err != nil {
		return "", err
	}
	rows, err := r.query(`SELECT active_session_id FROM conversations WHERE conversation_id = ` + q(conversationID) + `;`)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return stringField(rows[0], "active_session_id"), nil
}

func (r *Repository) GetHistory(_ context.Context, conversationID, sessionID string, limit int) ([]types.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.query(fmt.Sprintf(`SELECT id, conversation_id, session_id, sender, recipient, role, kind, chat_type, text, mentioned_bot, created_at
		FROM (
			SELECT id, conversation_id, session_id, sender, recipient, role, kind, chat_type, text, mentioned_bot, created_at
			FROM messages
			WHERE conversation_id = %s AND session_id = %s
			ORDER BY datetime(created_at) DESC
			LIMIT %d
		)
		ORDER BY datetime(created_at) ASC;`, q(conversationID), q(sessionID), limit))
	if err != nil {
		return nil, err
	}
	out := make([]types.Message, 0, len(rows))
	for _, row := range rows {
		out = append(out, decodeMessage(row))
	}
	return out, nil
}

func (r *Repository) GetActiveHistory(ctx context.Context, conversationID string, limit int) ([]types.Message, error) {
	sessionID, err := r.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	return r.GetHistory(ctx, conversationID, sessionID, limit)
}

func (r *Repository) SaveConversationSummary(_ context.Context, conversationID, sessionID, summary string) error {
	if err := r.ensureConversation(conversationID); err != nil {
		return err
	}
	if err := r.ensureSession(conversationID, sessionID); err != nil {
		return err
	}
	_, err := r.exec(`UPDATE sessions SET summary = ` + q(summary) + ` WHERE conversation_id = ` + q(conversationID) + ` AND session_id = ` + q(sessionID) + `;`)
	return err
}

func (r *Repository) GetConversationSummary(_ context.Context, conversationID, sessionID string) (string, error) {
	rows, err := r.query(`SELECT summary FROM sessions WHERE conversation_id = ` + q(conversationID) + ` AND session_id = ` + q(sessionID) + `;`)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return stringField(rows[0], "summary"), nil
}

func (r *Repository) GetActiveConversationSummary(ctx context.Context, conversationID string) (string, error) {
	sessionID, err := r.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		return "", err
	}
	return r.GetConversationSummary(ctx, conversationID, sessionID)
}

func (r *Repository) SaveSessionPersona(_ context.Context, conversationID, sessionID, persona string) error {
	if err := r.ensureConversation(conversationID); err != nil {
		return err
	}
	if err := r.ensureSession(conversationID, sessionID); err != nil {
		return err
	}
	_, err := r.exec(`UPDATE sessions SET persona = ` + q(persona) + ` WHERE conversation_id = ` + q(conversationID) + ` AND session_id = ` + q(sessionID) + `;`)
	return err
}

func (r *Repository) GetSessionPersona(_ context.Context, conversationID, sessionID string) (string, error) {
	rows, err := r.query(`SELECT persona FROM sessions WHERE conversation_id = ` + q(conversationID) + ` AND session_id = ` + q(sessionID) + `;`)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return stringField(rows[0], "persona"), nil
}

func (r *Repository) GetActiveSessionPersona(ctx context.Context, conversationID string) (string, error) {
	sessionID, err := r.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		return "", err
	}
	return r.GetSessionPersona(ctx, conversationID, sessionID)
}

func (r *Repository) SaveSessionFormat(_ context.Context, conversationID, sessionID, format string) error {
	if err := r.ensureConversation(conversationID); err != nil {
		return err
	}
	if err := r.ensureSession(conversationID, sessionID); err != nil {
		return err
	}
	_, err := r.exec(`UPDATE sessions SET format = ` + q(format) + ` WHERE conversation_id = ` + q(conversationID) + ` AND session_id = ` + q(sessionID) + `;`)
	return err
}

func (r *Repository) GetSessionFormat(_ context.Context, conversationID, sessionID string) (string, error) {
	rows, err := r.query(`SELECT format FROM sessions WHERE conversation_id = ` + q(conversationID) + ` AND session_id = ` + q(sessionID) + `;`)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return stringField(rows[0], "format"), nil
}

func (r *Repository) GetActiveSessionFormat(ctx context.Context, conversationID string) (string, error) {
	sessionID, err := r.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		return "", err
	}
	return r.GetSessionFormat(ctx, conversationID, sessionID)
}

func (r *Repository) SaveSessionFallbackDisabled(_ context.Context, conversationID, sessionID string, disabled bool) error {
	if err := r.ensureConversation(conversationID); err != nil {
		return err
	}
	if err := r.ensureSession(conversationID, sessionID); err != nil {
		return err
	}
	_, err := r.exec(`UPDATE sessions SET fallback_disabled = ` + strconv.Itoa(boolInt(disabled)) + ` WHERE conversation_id = ` + q(conversationID) + ` AND session_id = ` + q(sessionID) + `;`)
	return err
}

func (r *Repository) GetSessionFallbackDisabled(_ context.Context, conversationID, sessionID string) (bool, error) {
	rows, err := r.query(`SELECT fallback_disabled FROM sessions WHERE conversation_id = ` + q(conversationID) + ` AND session_id = ` + q(sessionID) + `;`)
	if err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil
	}
	return intField(rows[0], "fallback_disabled") != 0, nil
}

func (r *Repository) GetActiveSessionFallbackDisabled(ctx context.Context, conversationID string) (bool, error) {
	sessionID, err := r.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		return false, err
	}
	return r.GetSessionFallbackDisabled(ctx, conversationID, sessionID)
}

func (r *Repository) RotateConversationSession(_ context.Context, conversationID string) (string, error) {
	if err := r.ensureConversation(conversationID); err != nil {
		return "", err
	}
	sessionID := "session_" + randomSuffix()
	if err := r.ensureSession(conversationID, sessionID); err != nil {
		return "", err
	}
	_, err := r.exec(`UPDATE conversations SET active_session_id = ` + q(sessionID) + ` WHERE conversation_id = ` + q(conversationID) + `;` +
		`UPDATE pending_approvals SET is_active = 0 WHERE conversation_id = ` + q(conversationID) + `;`)
	return sessionID, err
}

func (r *Repository) SavePendingApproval(_ context.Context, conversationID, sessionID string, approval types.PendingApproval) error {
	if err := r.ensureConversation(conversationID); err != nil {
		return err
	}
	if err := r.ensureSession(conversationID, sessionID); err != nil {
		return err
	}
	sql := fmt.Sprintf(`UPDATE pending_approvals SET is_active = 0 WHERE conversation_id = %s;
INSERT OR REPLACE INTO pending_approvals
	(request_id, conversation_id, session_id, command_text, proposed_by, nonce, created_at, expires_at, is_active)
	VALUES (%s,%s,%s,%s,%s,%s,%s,%s,1);`,
		q(conversationID),
		q(approval.RequestID), q(conversationID), q(sessionID), q(approval.Command), q(approval.ProposedBy), q(approval.Nonce),
		q(approval.CreatedAt.Format(timeLayout)), q(approval.ExpiresAt.Format(timeLayout)))
	_, err := r.exec(sql)
	return err
}

func (r *Repository) GetPendingApproval(_ context.Context, conversationID, requestID string) (*types.PendingApproval, error) {
	rows, err := r.query(`SELECT request_id, conversation_id, session_id, command_text, proposed_by, nonce, created_at, expires_at
		FROM pending_approvals WHERE conversation_id = ` + q(conversationID) + ` AND request_id = ` + q(requestID) + ` LIMIT 1;`)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	approval := decodeApproval(rows[0])
	return &approval, nil
}

func (r *Repository) GetActivePendingApproval(_ context.Context, conversationID string) (*types.PendingApproval, error) {
	rows, err := r.query(`SELECT request_id, conversation_id, session_id, command_text, proposed_by, nonce, created_at, expires_at
		FROM pending_approvals WHERE conversation_id = ` + q(conversationID) + ` AND is_active = 1 LIMIT 1;`)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	approval := decodeApproval(rows[0])
	return &approval, nil
}

func (r *Repository) ClearPendingApproval(_ context.Context, conversationID, requestID string) error {
	_, err := r.exec(`UPDATE pending_approvals SET is_active = 0 WHERE conversation_id = ` + q(conversationID) + ` AND request_id = ` + q(requestID) + `;`)
	return err
}

func (r *Repository) ensureConversation(conversationID string) error {
	rows, err := r.query(`SELECT active_session_id FROM conversations WHERE conversation_id = ` + q(conversationID) + ` LIMIT 1;`)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		return nil
	}
	sessionID := "session_" + randomSuffix()
	_, err = r.exec(`INSERT INTO conversations (conversation_id, active_session_id) VALUES (` + q(conversationID) + `, ` + q(sessionID) + `);` +
		`INSERT INTO sessions (conversation_id, session_id, summary, persona, format, fallback_disabled) VALUES (` + q(conversationID) + `, ` + q(sessionID) + `, '', '', '', 0);`)
	return err
}

func (r *Repository) ensureSession(conversationID, sessionID string) error {
	_, err := r.exec(`INSERT OR IGNORE INTO sessions (conversation_id, session_id, summary, persona, format, fallback_disabled) VALUES (` + q(conversationID) + `, ` + q(sessionID) + `, '', '', '', 0);`)
	return err
}

func (r *Repository) ensureSessionPersonaColumn() error {
	rows, err := r.query(`PRAGMA table_info(sessions);`)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if stringField(row, "name") == "persona" {
			return nil
		}
	}
	_, err = r.exec(`ALTER TABLE sessions ADD COLUMN persona TEXT NOT NULL DEFAULT '';`)
	return err
}

func (r *Repository) ensureSessionFormatColumn() error {
	rows, err := r.query(`PRAGMA table_info(sessions);`)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if stringField(row, "name") == "format" {
			return nil
		}
	}
	_, err = r.exec(`ALTER TABLE sessions ADD COLUMN format TEXT NOT NULL DEFAULT '';`)
	return err
}

func (r *Repository) ensureSessionFallbackDisabledColumn() error {
	rows, err := r.query(`PRAGMA table_info(sessions);`)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if stringField(row, "name") == "fallback_disabled" {
			return nil
		}
	}
	_, err = r.exec(`ALTER TABLE sessions ADD COLUMN fallback_disabled INTEGER NOT NULL DEFAULT 0;`)
	return err
}

func (r *Repository) exec(sql string) (string, error) {
	cmd := exec.Command("sqlite3", r.dsn, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("sqlite exec: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (r *Repository) query(sql string) ([]map[string]any, error) {
	cmd := exec.Command("sqlite3", "-json", r.dsn, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sqlite query: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("decode sqlite json: %w", err)
	}
	return rows, nil
}

const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func q(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func stringField(row map[string]any, key string) string {
	if v, ok := row[key]; ok {
		switch vv := v.(type) {
		case string:
			return vv
		case float64:
			return strconv.FormatFloat(vv, 'f', -1, 64)
		}
	}
	return ""
}

func intField(row map[string]any, key string) int {
	text := stringField(row, key)
	if text == "" {
		return 0
	}
	value, err := strconv.Atoi(text)
	if err != nil {
		return 0
	}
	return value
}

func decodeMessage(row map[string]any) types.Message {
	createdAt, _ := parseTime(stringField(row, "created_at"))
	return types.Message{
		ID:             stringField(row, "id"),
		ConversationID: stringField(row, "conversation_id"),
		SessionID:      stringField(row, "session_id"),
		Sender:         stringField(row, "sender"),
		Recipient:      stringField(row, "recipient"),
		Role:           types.Role(stringField(row, "role")),
		Kind:           types.MessageKind(stringField(row, "kind")),
		ChatType:       types.ChatType(stringField(row, "chat_type")),
		Text:           stringField(row, "text"),
		MentionedBot:   stringField(row, "mentioned_bot") == "1",
		CreatedAt:      createdAt,
	}
}

func decodeApproval(row map[string]any) types.PendingApproval {
	createdAt, _ := parseTime(stringField(row, "created_at"))
	expiresAt, _ := parseTime(stringField(row, "expires_at"))
	return types.PendingApproval{
		RequestID:      stringField(row, "request_id"),
		ConversationID: stringField(row, "conversation_id"),
		SessionID:      stringField(row, "session_id"),
		Command:        stringField(row, "command_text"),
		ProposedBy:     stringField(row, "proposed_by"),
		Nonce:          stringField(row, "nonce"),
		CreatedAt:      createdAt,
		ExpiresAt:      expiresAt,
	}
}

func parseTime(v string) (t typesTime, err error) { return parseStdTime(v) }
