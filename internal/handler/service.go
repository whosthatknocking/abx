package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/whosthatknocking/abx/internal/agent"
	"github.com/whosthatknocking/abx/internal/audit"
	"github.com/whosthatknocking/abx/internal/config"
	"github.com/whosthatknocking/abx/internal/repository"
	"github.com/whosthatknocking/abx/pkg/types"
)

type messenger interface {
	Start(ctx context.Context, handler func(types.IncomingEnvelope)) error
	Send(ctx context.Context, recipient, text string) error
}

type commandExecutor interface {
	Execute(ctx context.Context, command string) (string, error)
}

type Options struct {
	Version   string
	Config    *config.Config
	Logger    *log.Logger
	Repo      repository.Repository
	Auditor   *audit.Logger
	Messenger messenger
	Agent     agent.Provider
	Executor  commandExecutor
}

type Service struct {
	version   string
	config    *config.Config
	logger    *log.Logger
	repo      repository.Repository
	auditor   *audit.Logger
	messenger messenger
	agent     agent.Provider
	executor  commandExecutor
}

const (
	recentHistoryLimit  = 20
	summaryHistoryLimit = 100
	summaryMaxChars     = 2000
)

func NewService(opts Options) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Service{
		version:   opts.Version,
		config:    opts.Config,
		logger:    logger,
		repo:      opts.Repo,
		auditor:   opts.Auditor,
		messenger: opts.Messenger,
		agent:     opts.Agent,
		executor:  opts.Executor,
	}
}

func (s *Service) Start(ctx context.Context) error {
	return s.messenger.Start(ctx, func(msg types.IncomingEnvelope) {
		if err := s.HandleMessage(ctx, msg); err != nil {
			s.logger.Printf("handle message conversation=%s sender=%s err=%v", msg.ConversationID, msg.Sender, err)
		}
	})
}

func (s *Service) HandleMessage(ctx context.Context, env types.IncomingEnvelope) error {
	if !s.allowed(env) {
		s.logger.Printf("untrusted interaction rejected conversation=%s sender=%s chat_type=%s mentioned=%t", env.ConversationID, env.Sender, env.ChatType, env.MentionedBot)
		s.audit(audit.Record{
			Event:          "message_rejected",
			ConversationID: env.ConversationID,
			Sender:         env.Sender,
			MessageType:    "inbound",
			Decision:       "untrusted_or_not_mentioned",
		})
		return nil
	}
	s.logger.Printf("interaction accepted conversation=%s sender=%s chat_type=%s mentioned=%t text_len=%d", env.ConversationID, env.Sender, env.ChatType, env.MentionedBot, len(strings.TrimSpace(env.Text)))

	sessionID, err := s.repo.GetActiveSessionID(ctx, env.ConversationID)
	if err != nil {
		return err
	}
	inbound := types.Message{
		ID:             coalesce(env.ID, mustID()),
		ConversationID: env.ConversationID,
		SessionID:      sessionID,
		Sender:         env.Sender,
		Recipient:      env.Recipient,
		Role:           types.RoleUser,
		Kind:           types.MessageKindInbound,
		ChatType:       env.ChatType,
		Text:           env.Text,
		MentionedBot:   env.MentionedBot,
		CreatedAt:      coalesceTime(env.CreatedAt, time.Now()),
	}
	if err := s.repo.SaveMessage(ctx, env.ConversationID, sessionID, inbound); err != nil {
		return err
	}
	s.audit(audit.Record{
		Event:          "message_received",
		ConversationID: env.ConversationID,
		SessionID:      sessionID,
		Sender:         env.Sender,
		Recipient:      env.Recipient,
		MessageType:    "inbound",
	})

	if err := s.cancelPendingApprovalOnNonApproval(ctx, env, sessionID); err != nil {
		return err
	}
	if s.isApproval(env.Text) {
		return s.handleApproval(ctx, env, sessionID)
	}
	if command, ok := s.shellRequest(env.Text); ok {
		return s.handleShellRequest(ctx, env, sessionID, command)
	}
	if isRunHelp(env.Text) {
		return s.handleRunHelp(ctx, env, sessionID)
	}
	if strings.HasPrefix(strings.TrimSpace(env.Text), "/") {
		return s.handleControl(ctx, env)
	}
	return s.handleConversation(ctx, env, sessionID)
}

func (s *Service) cancelPendingApprovalOnNonApproval(ctx context.Context, env types.IncomingEnvelope, sessionID string) error {
	if s.isApproval(env.Text) {
		return nil
	}
	approval, err := s.repo.GetActivePendingApproval(ctx, env.ConversationID)
	if err != nil {
		return err
	}
	if approval == nil {
		return nil
	}
	if err := s.repo.ClearPendingApproval(ctx, env.ConversationID, approval.RequestID); err != nil {
		return err
	}
	s.audit(audit.Record{
		Event:          "approval_cancelled",
		ConversationID: env.ConversationID,
		SessionID:      approval.SessionID,
		Sender:         env.Sender,
		RequestID:      approval.RequestID,
		MessageType:    "approval",
		Decision:       "cancelled_by_other_reply",
	})
	return nil
}

func (s *Service) allowed(env types.IncomingEnvelope) bool {
	if !isTrusted(env.Sender, s.config.Security.TrustedNumbers) {
		return false
	}
	if env.ChatType == types.ChatTypeGroup && !env.MentionedBot {
		return false
	}
	return true
}

func (s *Service) handleConversation(ctx context.Context, env types.IncomingEnvelope, sessionID string) error {
	history, err := s.repo.GetActiveHistory(ctx, env.ConversationID, summaryHistoryLimit)
	if err != nil {
		return err
	}
	history = normalizeHistoryForAgent(history)
	summary, err := s.repo.GetActiveConversationSummary(ctx, env.ConversationID)
	if err != nil {
		return err
	}
	olderHistory, recentHistory := splitHistoryForSummary(history, recentHistoryLimit)
	desiredSummary := summarizeMessages(olderHistory, summaryMaxChars)
	if desiredSummary != summary {
		if err := s.repo.SaveConversationSummary(ctx, env.ConversationID, sessionID, desiredSummary); err != nil {
			return err
		}
		summary = desiredSummary
	}
	agentMessages := prependSummaryMessage(recentHistory, summary)
	s.logger.Printf(
		"agent interaction start conversation=%s session=%s sender=%s history_messages=%d history_chars=%d summary_chars=%d input_chars=%d",
		env.ConversationID,
		sessionID,
		env.Sender,
		len(agentMessages),
		totalChars(agentMessages),
		len(summary),
		len(strings.TrimSpace(env.Text)),
	)
	response, err := s.agent.Chat(ctx, agentMessages, nil)
	if err != nil {
		s.logger.Printf("agent interaction failed conversation=%s session=%s sender=%s err=%v", env.ConversationID, sessionID, env.Sender, err)
		s.audit(audit.Record{
			Event:          "agent_error",
			ConversationID: env.ConversationID,
			SessionID:      sessionID,
			Sender:         env.Sender,
			MessageType:    "conversation",
			Error:          err.Error(),
		})
		return s.messenger.Send(ctx, env.ConversationID, fmt.Sprintf("Agent request failed: %v", err))
	}
	s.logger.Printf(
		"agent interaction complete conversation=%s session=%s sender=%s response_chars=%d",
		env.ConversationID,
		sessionID,
		env.Sender,
		len(strings.TrimSpace(response.Text)),
	)
	return s.sendAssistantDisplay(ctx, env.ConversationID, sessionID, env.ChatType, strings.TrimSpace(response.Text), s.formatAgentReply(response.Text))
}

func (s *Service) handleShellRequest(ctx context.Context, env types.IncomingEnvelope, sessionID, command string) error {
	s.logger.Printf("command proposal created conversation=%s session=%s sender=%s command=%q", env.ConversationID, sessionID, env.Sender, command)
	requestID := mustID()
	nonce := mustToken(3)
	approval := types.PendingApproval{
		RequestID:      requestID,
		ConversationID: env.ConversationID,
		SessionID:      sessionID,
		Command:        command,
		ProposedBy:     env.Sender,
		Nonce:          nonce,
		CreatedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(5 * time.Minute),
	}
	if err := s.repo.SavePendingApproval(ctx, env.ConversationID, sessionID, approval); err != nil {
		return err
	}
	s.audit(audit.Record{
		Event:          "command_proposed",
		ConversationID: env.ConversationID,
		SessionID:      sessionID,
		Sender:         env.Sender,
		RequestID:      requestID,
		MessageType:    "command",
		Command:        command,
		Decision:       "pending_approval",
	})
	text := fmt.Sprintf("Proposed command:\n%s\n\nReply with: YES %s", command, nonce)
	return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, text)
}

func (s *Service) handleRunHelp(ctx context.Context, env types.IncomingEnvelope, sessionID string) error {
	s.audit(audit.Record{
		Event:          "control_command",
		ConversationID: env.ConversationID,
		SessionID:      sessionID,
		Sender:         env.Sender,
		MessageType:    "control",
		Decision:       "/run",
	})
	return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Usage: /run <command>\nExample: /run pwd")
}

func (s *Service) handleApproval(ctx context.Context, env types.IncomingEnvelope, sessionID string) error {
	approval, err := s.repo.GetActivePendingApproval(ctx, env.ConversationID)
	if err != nil {
		return err
	}
	if approval == nil {
		s.audit(audit.Record{
			Event:          "approval_missing",
			ConversationID: env.ConversationID,
			SessionID:      sessionID,
			Sender:         env.Sender,
			MessageType:    "approval",
			Decision:       "no_active_request",
		})
		return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "There is no active pending approval for this chat.")
	}
	if time.Now().After(approval.ExpiresAt) {
		if err := s.repo.ClearPendingApproval(ctx, env.ConversationID, approval.RequestID); err != nil {
			return err
		}
		s.audit(audit.Record{
			Event:          "approval_expired",
			ConversationID: env.ConversationID,
			SessionID:      approval.SessionID,
			Sender:         env.Sender,
			RequestID:      approval.RequestID,
			MessageType:    "approval",
			ApprovalText:   strings.TrimSpace(env.Text),
			Decision:       "expired",
		})
		return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "The pending approval token has expired.")
	}
	nonce := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(env.Text), "YES"))
	if nonce != approval.Nonce {
		s.audit(audit.Record{
			Event:          "approval_rejected",
			ConversationID: env.ConversationID,
			SessionID:      approval.SessionID,
			Sender:         env.Sender,
			RequestID:      approval.RequestID,
			MessageType:    "approval",
			ApprovalText:   strings.TrimSpace(env.Text),
			Decision:       "token_mismatch",
		})
		return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Approval token mismatch.")
	}
	s.logger.Printf("command approval accepted conversation=%s session=%s request_id=%s proposed_by=%s approved_by=%s command=%q", env.ConversationID, approval.SessionID, approval.RequestID, approval.ProposedBy, env.Sender, approval.Command)
	output, execErr := s.executor.Execute(ctx, approval.Command)
	if clearErr := s.repo.ClearPendingApproval(ctx, env.ConversationID, approval.RequestID); clearErr != nil {
		return clearErr
	}
	s.audit(audit.Record{
		Event:          "command_executed",
		ConversationID: env.ConversationID,
		SessionID:      approval.SessionID,
		Sender:         approval.ProposedBy,
		ApprovedBy:     env.Sender,
		RequestID:      approval.RequestID,
		MessageType:    "command",
		ApprovalText:   strings.TrimSpace(env.Text),
		Command:        approval.Command,
		Decision:       decisionFor(execErr),
		Output:         output,
		Error:          errorString(execErr),
		ExitStatus:     exitStatus(execErr),
	})
	if execErr != nil {
		s.logger.Printf("command execution failed conversation=%s session=%s request_id=%s err=%v output_chars=%d", env.ConversationID, approval.SessionID, approval.RequestID, execErr, len(output))
		return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, fmt.Sprintf("Command failed:\n%s", strings.TrimSpace(execErr.Error()+"\n"+output)))
	}
	s.logger.Printf("command execution complete conversation=%s session=%s request_id=%s output_chars=%d", env.ConversationID, approval.SessionID, approval.RequestID, len(output))
	return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, fmt.Sprintf("Command completed.\n%s", strings.TrimSpace(output)))
}

func (s *Service) handleControl(ctx context.Context, env types.IncomingEnvelope) error {
	sessionID, err := s.repo.GetActiveSessionID(ctx, env.ConversationID)
	if err != nil {
		return err
	}
	fields := strings.Fields(strings.TrimSpace(env.Text))
	if len(fields) == 0 {
		return nil
	}
	switch fields[0] {
	case "/version":
		s.audit(audit.Record{
			Event:          "control_command",
			ConversationID: env.ConversationID,
			SessionID:      sessionID,
			Sender:         env.Sender,
			MessageType:    "control",
			Decision:       "/version",
		})
		return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "abx version "+s.version)
	case "/config":
		fallback := "not configured"
		if s.config.Agent.Fallback.Provider != "" {
			fallback = fmt.Sprintf("%s (%s)", s.config.Agent.Fallback.Model, endpointClass(s.config.Agent.Fallback.BaseURL))
		}
		text := fmt.Sprintf("Primary: %s (%s)\nFallback: %s\nVersion: %s",
			s.config.Agent.Primary.Model, endpointClass(s.config.Agent.Primary.BaseURL), fallback, s.version)
		s.audit(audit.Record{
			Event:          "control_command",
			ConversationID: env.ConversationID,
			SessionID:      sessionID,
			Sender:         env.Sender,
			MessageType:    "control",
			Decision:       "/config",
		})
		return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, text)
	case "/reset":
		active, err := s.repo.GetActivePendingApproval(ctx, env.ConversationID)
		if err == nil && active != nil {
			if err := s.repo.ClearPendingApproval(ctx, env.ConversationID, active.RequestID); err != nil {
				return err
			}
		}
		if _, err := s.repo.RotateConversationSession(ctx, env.ConversationID); err != nil {
			return err
		}
		newSessionID, err := s.repo.GetActiveSessionID(ctx, env.ConversationID)
		if err != nil {
			return err
		}
		s.audit(audit.Record{
			Event:          "control_command",
			ConversationID: env.ConversationID,
			SessionID:      newSessionID,
			Sender:         env.Sender,
			MessageType:    "control",
			Decision:       "/reset",
		})
		return s.sendAssistant(ctx, env.ConversationID, newSessionID, env.ChatType, "Conversation context reset for this chat.")
	default:
		return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Unknown control command.")
	}
}

func (s *Service) sendAssistant(ctx context.Context, conversationID, sessionID string, chatType types.ChatType, text string) error {
	return s.sendAssistantDisplay(ctx, conversationID, sessionID, chatType, text, text)
}

func (s *Service) sendAssistantDisplay(ctx context.Context, conversationID, sessionID string, chatType types.ChatType, storedText, displayText string) error {
	msg := types.Message{
		ID:             mustID(),
		ConversationID: conversationID,
		SessionID:      sessionID,
		Role:           types.RoleAssistant,
		Kind:           types.MessageKindOutbound,
		ChatType:       chatType,
		Text:           storedText,
		CreatedAt:      time.Now(),
	}
	if err := s.repo.SaveMessage(ctx, conversationID, sessionID, msg); err != nil {
		return err
	}
	s.audit(audit.Record{
		Event:          "message_sent",
		ConversationID: conversationID,
		SessionID:      sessionID,
		MessageType:    "outbound",
		Output:         displayText,
	})
	return s.messenger.Send(ctx, conversationID, displayText)
}

func (s *Service) shellRequest(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "/run ") {
		return strings.TrimSpace(strings.TrimPrefix(text, "/run ")), true
	}
	return "", false
}

func isRunHelp(text string) bool {
	return strings.TrimSpace(text) == "/run"
}

func (s *Service) isApproval(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "YES ")
}

func isTrusted(sender string, trusted []string) bool {
	for _, value := range trusted {
		if value == sender {
			return true
		}
	}
	return false
}

func mustID() string {
	return mustToken(8)
}

func mustToken(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

func coalesce(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func coalesceTime(value, fallback time.Time) time.Time {
	if !value.IsZero() {
		return value
	}
	return fallback
}

func (s *Service) audit(record audit.Record) {
	if s.auditor == nil {
		return
	}
	if err := s.auditor.Log(record); err != nil {
		s.logger.Printf("audit log error: %v", err)
	}
}

func decisionFor(err error) string {
	if err != nil {
		return "failed"
	}
	return "approved_and_executed"
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func exitStatus(err error) *int {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return nil
	}
	code := exitErr.ExitCode()
	return &code
}

func totalChars(messages []types.Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Text)
	}
	return total
}

func splitHistoryForSummary(messages []types.Message, recentLimit int) ([]types.Message, []types.Message) {
	if recentLimit <= 0 || len(messages) <= recentLimit {
		return nil, append([]types.Message(nil), messages...)
	}
	cut := len(messages) - recentLimit
	older := append([]types.Message(nil), messages[:cut]...)
	recent := append([]types.Message(nil), messages[cut:]...)
	return older, recent
}

func prependSummaryMessage(messages []types.Message, summary string) []types.Message {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return append([]types.Message(nil), messages...)
	}
	out := make([]types.Message, 0, len(messages)+1)
	out = append(out, types.Message{
		Role: types.RoleSystem,
		Text: "Conversation summary:\n" + summary,
	})
	out = append(out, messages...)
	return out
}

func summarizeMessages(messages []types.Message, maxChars int) string {
	if len(messages) == 0 {
		return ""
	}
	sorted := append([]types.Message(nil), messages...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
	})

	var b strings.Builder
	for _, msg := range sorted {
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			continue
		}
		line := roleLabel(msg.Role) + ": " + compactWhitespace(text)
		if maxChars > 0 && b.Len()+len(line)+1 > maxChars {
			remaining := maxChars - b.Len()
			if remaining <= 0 {
				break
			}
			if remaining > 4 {
				b.WriteString(line[:remaining-3])
				b.WriteString("...")
			}
			break
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return strings.TrimSpace(b.String())
}

func roleLabel(role types.Role) string {
	switch role {
	case types.RoleAssistant:
		return "Assistant"
	case types.RoleSystem:
		return "System"
	default:
		return "User"
	}
}

func compactWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func (s *Service) formatAgentReply(text string) string {
	if s.config == nil || !s.config.Debug.Enabled {
		return text
	}
	label := fmt.Sprintf("[agent: %s / %s (%s)]", s.config.Agent.Primary.Provider, s.config.Agent.Primary.Model, endpointClass(s.config.Agent.Primary.BaseURL))
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return label
	}
	return trimmed + "\n\n" + label
}

var debugAgentSuffixPattern = regexp.MustCompile(`(?s)\n*\[agent:\s+[^\]]+\]\s*$`)

func normalizeHistoryForAgent(messages []types.Message) []types.Message {
	out := make([]types.Message, 0, len(messages))
	for _, msg := range messages {
		copy := msg
		copy.Text = strings.TrimSpace(debugAgentSuffixPattern.ReplaceAllString(copy.Text, ""))
		out = append(out, copy)
	}
	return out
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
