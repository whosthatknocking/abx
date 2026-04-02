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
	"sync"
	"time"

	"github.com/whosthatknocking/abx/internal/agent"
	"github.com/whosthatknocking/abx/internal/agent/openai"
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
	Check(command string) error
}

type Options struct {
	Version   string
	BuildInfo string
	Config    *config.Config
	Logger    *log.Logger
	Repo      repository.Repository
	Auditor   *audit.Logger
	Messenger messenger
	Agent     agent.Provider
	Executor  commandExecutor
	Reload    ReloadAgentsFunc
}

type ReloadAgents struct {
	Config *config.Config
	Agent  agent.Provider
}

type ReloadAgentsFunc func(ctx context.Context) (*ReloadAgents, error)

type Service struct {
	version   string
	buildInfo string
	runtimeMu sync.RWMutex
	config    *config.Config
	logger    *log.Logger
	repo      repository.Repository
	auditor   *audit.Logger
	messenger messenger
	agent     agent.Provider
	executor  commandExecutor
	reload    ReloadAgentsFunc
	locks     sync.Map
}

const (
	recentHistoryLimit  = 20
	summaryHistoryLimit = 100
	summaryMaxChars     = 2000
	commandContextChars = 1200
	personaMaxChars     = 500
	formatMaxChars      = 300
)

const defaultSessionFormat = "Respond in plain text format."
const thinkingModeDefault = "default"

func NewService(opts Options) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Service{
		version:   opts.Version,
		buildInfo: strings.TrimSpace(opts.BuildInfo),
		config:    opts.Config,
		logger:    logger,
		repo:      opts.Repo,
		auditor:   opts.Auditor,
		messenger: opts.Messenger,
		agent:     opts.Agent,
		executor:  opts.Executor,
		reload:    opts.Reload,
	}
}

func (s *Service) Start(ctx context.Context) error {
	var wg sync.WaitGroup
	err := s.messenger.Start(ctx, func(msg types.IncomingEnvelope) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.HandleMessage(ctx, msg); err != nil {
				s.logger.Printf("handle message conversation=%s sender=%s err=%v", msg.ConversationID, msg.Sender, err)
			}
		}()
	})
	wg.Wait()
	return err
}

func (s *Service) HandleMessage(ctx context.Context, env types.IncomingEnvelope) error {
	if s.isImmediateLocalControl(env) {
		return s.handleImmediateLocalControl(ctx, env)
	}
	lock := s.conversationLock(env.ConversationID)
	lock.Lock()
	defer lock.Unlock()
	return s.handleMessage(ctx, env)
}

func (s *Service) handleMessage(ctx context.Context, env types.IncomingEnvelope) error {
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
	text := effectiveIncomingText(env)
	s.logger.Printf("interaction accepted conversation=%s sender=%s chat_type=%s mentioned=%t text_len=%d", env.ConversationID, env.Sender, env.ChatType, env.MentionedBot, len(strings.TrimSpace(text)))

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
		Text:           text,
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
	if s.isApproval(text) {
		return s.handleApproval(ctx, env, sessionID)
	}
	if command, ok := s.shellRequest(text); ok {
		return s.handleRunRequest(ctx, env, sessionID, command)
	}
	if isRunHelp(text) {
		return s.handleRunHelp(ctx, env, sessionID)
	}
	if strings.HasPrefix(strings.TrimSpace(text), "/") {
		return s.handleControl(ctx, env)
	}
	return s.handleConversation(ctx, env, sessionID)
}

func (s *Service) conversationLock(conversationID string) *sync.Mutex {
	if conversationID == "" {
		return &sync.Mutex{}
	}
	lock, _ := s.locks.LoadOrStore(conversationID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (s *Service) isImmediateLocalControl(env types.IncomingEnvelope) bool {
	if !s.allowed(env) {
		return false
	}
	fields := strings.Fields(strings.TrimSpace(effectiveIncomingText(env)))
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "/help", "/version", "/reset":
		return true
	case "/config":
		return len(fields) == 1
	case "/agents":
		if len(fields) == 1 {
			return true
		}
		switch fields[1] {
		case "list", "status", "switch", "reload":
			return true
		case "persona", "format", "fallback", "thinking":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func (s *Service) handleImmediateLocalControl(ctx context.Context, env types.IncomingEnvelope) error {
	text := effectiveIncomingText(env)
	s.logger.Printf("interaction accepted conversation=%s sender=%s chat_type=%s mentioned=%t text_len=%d immediate_local=%t", env.ConversationID, env.Sender, env.ChatType, env.MentionedBot, len(strings.TrimSpace(text)), true)

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
		Text:           text,
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
	return s.handleControl(ctx, env)
}

func (s *Service) cancelPendingApprovalOnNonApproval(ctx context.Context, env types.IncomingEnvelope, sessionID string) error {
	if s.isApproval(effectiveIncomingText(env)) {
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
	cfg := s.runtimeConfig()
	if !isTrusted(env.Sender, cfg.Security.TrustedNumbers) {
		return false
	}
	if env.ChatType == types.ChatTypeGroup && !env.MentionedBot {
		return false
	}
	return true
}

func (s *Service) handleConversation(ctx context.Context, env types.IncomingEnvelope, sessionID string) error {
	history, err := s.repo.GetHistory(ctx, env.ConversationID, sessionID, summaryHistoryLimit)
	if err != nil {
		return err
	}
	history = normalizeHistoryForAgent(history)
	summary, err := s.repo.GetConversationSummary(ctx, env.ConversationID, sessionID)
	if err != nil {
		return err
	}
	persona, err := s.repo.GetSessionPersona(ctx, env.ConversationID, sessionID)
	if err != nil {
		return err
	}
	format, err := s.repo.GetSessionFormat(ctx, env.ConversationID, sessionID)
	if err != nil {
		return err
	}
	fallbackDisabled, err := s.repo.GetSessionFallbackDisabled(ctx, env.ConversationID, sessionID)
	if err != nil {
		return err
	}
	thinkingMode, err := s.repo.GetSessionThinkingMode(ctx, env.ConversationID, sessionID)
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
	agentMessages := prependConversationSystemPrompt(prependFormatMessage(prependPersonaMessage(prependSummaryMessage(recentHistory, summary), persona), effectiveSessionFormat(format)))
	s.logger.Printf(
		"agent interaction start conversation=%s session=%s sender=%s history_messages=%d history_chars=%d summary_chars=%d input_chars=%d",
		env.ConversationID,
		sessionID,
		env.Sender,
		len(agentMessages),
		totalChars(agentMessages),
		len(summary),
		len(strings.TrimSpace(effectiveIncomingText(env))),
	)
	response, err := s.chatWithSessionAgent(ctx, env.ConversationID, agentMessages, nil, fallbackDisabled, thinkingMode)
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
	if command, reason, ok := parseConversationCommandProposal(response.Text); ok {
		if err := s.runtimeExecutor().Check(command); err != nil {
			s.audit(audit.Record{
				Event:          "conversation_command_blocked",
				ConversationID: env.ConversationID,
				SessionID:      sessionID,
				Sender:         env.Sender,
				MessageType:    "command",
				Command:        command,
				Error:          err.Error(),
			})
			return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, formatBlockedRecommendation(command, reason, err))
		}
		return s.proposeCommand(ctx, env, sessionID, command, reason)
	}
	return s.sendAssistantDisplay(ctx, env.ConversationID, sessionID, env.ChatType, strings.TrimSpace(response.Text), s.formatAgentReply(response))
}

func (s *Service) handleRunRequest(ctx context.Context, env types.IncomingEnvelope, sessionID, input string) error {
	input = strings.TrimSpace(input)
	// Treat obviously command-shaped input as a direct command path so policy
	// errors are reported immediately instead of being reframed as agent intent.
	if looksLikeExactCommand(input) {
		if err := s.runtimeExecutor().Check(input); err != nil {
			return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, fmt.Sprintf("Command failed:\n%s", err))
		}
		return s.proposeCommand(ctx, env, sessionID, input, "")
	}
	if err := s.runtimeExecutor().Check(input); err == nil {
		return s.proposeCommand(ctx, env, sessionID, input, "")
	}
	return s.handleRecommendedRun(ctx, env, sessionID, input)
}

func (s *Service) handleRecommendedRun(ctx context.Context, env types.IncomingEnvelope, sessionID, input string) error {
	messages, err := s.runRecommendationMessages(ctx, env.ConversationID, sessionID)
	if err != nil {
		return err
	}
	s.logger.Printf("run recommendation requested conversation=%s session=%s sender=%s input=%q", env.ConversationID, sessionID, env.Sender, input)
	fallbackDisabled, err := s.repo.GetSessionFallbackDisabled(ctx, env.ConversationID, sessionID)
	if err != nil {
		return err
	}
	thinkingMode, err := s.repo.GetSessionThinkingMode(ctx, env.ConversationID, sessionID)
	if err != nil {
		return err
	}
	response, err := s.chatWithSessionAgent(ctx, env.ConversationID, messages, nil, fallbackDisabled, thinkingMode)
	if err != nil {
		s.audit(audit.Record{
			Event:          "run_recommendation_error",
			ConversationID: env.ConversationID,
			SessionID:      sessionID,
			Sender:         env.Sender,
			MessageType:    "command",
			Error:          err.Error(),
		})
		return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, fmt.Sprintf("Agent request failed: %v", err))
	}

	command, reason, ok := parseRunRecommendation(response.Text)
	if !ok {
		s.audit(audit.Record{
			Event:          "run_recommendation_invalid",
			ConversationID: env.ConversationID,
			SessionID:      sessionID,
			Sender:         env.Sender,
			MessageType:    "command",
			Error:          "agent did not return a valid command recommendation",
		})
		return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "I couldn't produce a runnable command recommendation for that request. Try a more specific `/run ...` request or provide the exact command.")
	}
	if err := s.runtimeExecutor().Check(command); err != nil {
		s.audit(audit.Record{
			Event:          "run_recommendation_blocked",
			ConversationID: env.ConversationID,
			SessionID:      sessionID,
			Sender:         env.Sender,
			MessageType:    "command",
			Command:        command,
			Error:          err.Error(),
		})
		return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, formatBlockedRecommendation(command, reason, err))
	}
	return s.proposeCommand(ctx, env, sessionID, command, reason)
}

func (s *Service) proposeCommand(ctx context.Context, env types.IncomingEnvelope, sessionID, command, reason string) error {
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
	text := formatCommandProposal(command, reason, nonce)
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
	return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Usage: /run <command-or-intent>\nExamples:\n/run pwd\n/run show the current user")
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
			ApprovalText:   strings.TrimSpace(effectiveIncomingText(env)),
			Decision:       "expired",
		})
		return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "The pending approval token has expired.")
	}
	nonce := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(effectiveIncomingText(env)), "YES"))
	if nonce != approval.Nonce {
		s.audit(audit.Record{
			Event:          "approval_rejected",
			ConversationID: env.ConversationID,
			SessionID:      approval.SessionID,
			Sender:         env.Sender,
			RequestID:      approval.RequestID,
			MessageType:    "approval",
			ApprovalText:   strings.TrimSpace(effectiveIncomingText(env)),
			Decision:       "token_mismatch",
		})
		return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Approval token mismatch.")
	}
	s.logger.Printf("command approval accepted conversation=%s session=%s request_id=%s proposed_by=%s approved_by=%s command=%q", env.ConversationID, approval.SessionID, approval.RequestID, approval.ProposedBy, env.Sender, approval.Command)
	if err := s.runtimeExecutor().Check(approval.Command); err != nil {
		if clearErr := s.repo.ClearPendingApproval(ctx, env.ConversationID, approval.RequestID); clearErr != nil {
			return clearErr
		}
		s.audit(audit.Record{
			Event:          "approval_rejected",
			ConversationID: env.ConversationID,
			SessionID:      approval.SessionID,
			Sender:         env.Sender,
			RequestID:      approval.RequestID,
			MessageType:    "approval",
			ApprovalText:   strings.TrimSpace(effectiveIncomingText(env)),
			Command:        approval.Command,
			Error:          err.Error(),
			Decision:       "policy_recheck_failed",
		})
		return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, fmt.Sprintf("Command failed:\n%s", err))
	}
	output, execErr := s.runtimeExecutor().Execute(ctx, approval.Command)
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
		ApprovalText:   strings.TrimSpace(effectiveIncomingText(env)),
		Command:        approval.Command,
		Decision:       decisionFor(execErr),
		Output:         output,
		Error:          errorString(execErr),
		ExitStatus:     exitStatus(execErr),
	})
	if execErr != nil {
		s.logger.Printf("command execution failed conversation=%s session=%s request_id=%s err=%v output_chars=%d", env.ConversationID, approval.SessionID, approval.RequestID, execErr, len(output))
		stored, display := formatCommandResultMessage(output, execErr)
		return s.sendAssistantDisplay(ctx, env.ConversationID, sessionID, env.ChatType, stored, display)
	}
	s.logger.Printf("command execution complete conversation=%s session=%s request_id=%s output_chars=%d", env.ConversationID, approval.SessionID, approval.RequestID, len(output))
	stored, display := formatCommandResultMessage(output, nil)
	return s.sendAssistantDisplay(ctx, env.ConversationID, sessionID, env.ChatType, stored, display)
}

func (s *Service) handleControl(ctx context.Context, env types.IncomingEnvelope) error {
	sessionID, err := s.repo.GetActiveSessionID(ctx, env.ConversationID)
	if err != nil {
		return err
	}
	fields := strings.Fields(strings.TrimSpace(effectiveIncomingText(env)))
	if len(fields) == 0 {
		return nil
	}
	if handled, err := s.handleReadOnlyControlMaybe(ctx, env, sessionID, fields[0]); handled {
		return err
	}
	switch fields[0] {
	case "/agents":
		if len(fields) < 2 {
			return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Usage: /agents <list|status|reload|switch|persona|format|thinking|fallback>")
		}
		switch fields[1] {
		case "list":
			return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, s.agentsListText())
		case "status":
			return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, s.agentsStatusText(ctx, env.ConversationID, sessionID))
		case "reload":
			return s.handleAgentsReload(ctx, env, sessionID)
		case "persona":
			if len(fields) == 2 {
				persona, err := s.repo.GetSessionPersona(ctx, env.ConversationID, sessionID)
				if err != nil {
					return err
				}
				if strings.TrimSpace(persona) == "" {
					return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "No persona is set for this session.")
				}
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Current persona:\n"+strings.TrimSpace(persona))
			}
			if fields[2] == "reset" {
				if err := s.repo.SaveSessionPersona(ctx, env.ConversationID, sessionID, ""); err != nil {
					return err
				}
				s.audit(audit.Record{
					Event:          "control_command",
					ConversationID: env.ConversationID,
					SessionID:      sessionID,
					Sender:         env.Sender,
					MessageType:    "control",
					Decision:       "/agents persona reset",
				})
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Persona cleared for this session.")
			}
			persona := normalizePersona(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(effectiveIncomingText(env)), "/agents persona")))
			if persona == "" {
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Usage: /agents persona <instruction>\nUse `/agents persona reset` to clear it.")
			}
			if err := s.repo.SaveSessionPersona(ctx, env.ConversationID, sessionID, persona); err != nil {
				return err
			}
			s.audit(audit.Record{
				Event:          "control_command",
				ConversationID: env.ConversationID,
				SessionID:      sessionID,
				Sender:         env.Sender,
				MessageType:    "control",
				Decision:       "/agents persona set",
			})
			return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Persona updated for this session.")
		case "format":
			if len(fields) == 2 {
				format, err := s.repo.GetSessionFormat(ctx, env.ConversationID, sessionID)
				if err != nil {
					return err
				}
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Current format:\n"+effectiveSessionFormat(format))
			}
			if fields[2] == "reset" {
				if err := s.repo.SaveSessionFormat(ctx, env.ConversationID, sessionID, ""); err != nil {
					return err
				}
				s.audit(audit.Record{
					Event:          "control_command",
					ConversationID: env.ConversationID,
					SessionID:      sessionID,
					Sender:         env.Sender,
					MessageType:    "control",
					Decision:       "/agents format reset",
				})
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Format reset for this session.")
			}
			format := normalizeFormat(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(effectiveIncomingText(env)), "/agents format")))
			if format == "" {
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Usage: /agents format <instruction>\nUse `/agents format reset` to return to plain text.")
			}
			if err := s.repo.SaveSessionFormat(ctx, env.ConversationID, sessionID, format); err != nil {
				return err
			}
			s.audit(audit.Record{
				Event:          "control_command",
				ConversationID: env.ConversationID,
				SessionID:      sessionID,
				Sender:         env.Sender,
				MessageType:    "control",
				Decision:       "/agents format set",
			})
			return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Format updated for this session.")
		case "thinking":
			if !thinkingConfiguredForAnyAgent(s.runtimeConfig()) {
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Thinking control is not configured for the active agents.")
			}
			if len(fields) == 2 {
				mode, err := s.repo.GetSessionThinkingMode(ctx, env.ConversationID, sessionID)
				if err != nil {
					return err
				}
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Current thinking mode:\n"+sessionThinkingModeText(s.runtimeConfig(), mode))
			}
			switch normalizeThinkingModeValue(fields[2]) {
			case "enabled":
				if err := s.repo.SaveSessionThinkingMode(ctx, env.ConversationID, sessionID, "enabled"); err != nil {
					return err
				}
				s.audit(audit.Record{
					Event:          "control_command",
					ConversationID: env.ConversationID,
					SessionID:      sessionID,
					Sender:         env.Sender,
					MessageType:    "control",
					Decision:       "/agents thinking enable",
				})
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Thinking enabled for this session.")
			case "disabled":
				if err := s.repo.SaveSessionThinkingMode(ctx, env.ConversationID, sessionID, "disabled"); err != nil {
					return err
				}
				s.audit(audit.Record{
					Event:          "control_command",
					ConversationID: env.ConversationID,
					SessionID:      sessionID,
					Sender:         env.Sender,
					MessageType:    "control",
					Decision:       "/agents thinking disable",
				})
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Thinking disabled for this session.")
			default:
				if strings.EqualFold(fields[2], "reset") || strings.EqualFold(fields[2], "default") {
					if err := s.repo.SaveSessionThinkingMode(ctx, env.ConversationID, sessionID, ""); err != nil {
						return err
					}
					s.audit(audit.Record{
						Event:          "control_command",
						ConversationID: env.ConversationID,
						SessionID:      sessionID,
						Sender:         env.Sender,
						MessageType:    "control",
						Decision:       "/agents thinking reset",
					})
					return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Thinking mode reset to the agent default for this session.")
				}
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Usage: /agents thinking <enable|disable|reset>")
			}
		case "fallback":
			cfg := s.runtimeConfig()
			if !fallbackConfigured(cfg) {
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Fallback agent is not configured.")
			}
			if len(fields) == 2 {
				disabled, err := s.repo.GetSessionFallbackDisabled(ctx, env.ConversationID, sessionID)
				if err != nil {
					return err
				}
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Current fallback mode:\n"+sessionFallbackModeText(disabled))
			}
			switch fields[2] {
			case "disable":
				if err := s.repo.SaveSessionFallbackDisabled(ctx, env.ConversationID, sessionID, true); err != nil {
					return err
				}
				s.audit(audit.Record{
					Event:          "control_command",
					ConversationID: env.ConversationID,
					SessionID:      sessionID,
					Sender:         env.Sender,
					MessageType:    "control",
					Decision:       "/agents fallback disable",
				})
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Fallback disabled for this session.")
			case "enable":
				if err := s.repo.SaveSessionFallbackDisabled(ctx, env.ConversationID, sessionID, false); err != nil {
					return err
				}
				s.audit(audit.Record{
					Event:          "control_command",
					ConversationID: env.ConversationID,
					SessionID:      sessionID,
					Sender:         env.Sender,
					MessageType:    "control",
					Decision:       "/agents fallback enable",
				})
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Fallback enabled for this session.")
			default:
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Usage: /agents fallback <enable|disable>")
			}
		case "switch":
			cfg, runtimeAgent, runtimeExecutor := s.runtimeSnapshot()
			if !fallbackConfigured(cfg) {
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Fallback agent is not configured.")
			}
			switcher, ok := runtimeAgent.(agent.Switcher)
			if !ok {
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Agent switching is not supported by the current runtime.")
			}
			if err := switcher.SwapPrimaryAndFallback(); err != nil {
				return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, fmt.Sprintf("Agent switch failed: %v", err))
			}
			nextCfg := *cfg
			nextCfg.Agent.Primary, nextCfg.Agent.Fallback = cfg.Agent.Fallback, cfg.Agent.Primary
			s.setRuntime(&nextCfg, runtimeAgent, runtimeExecutor)
			s.audit(audit.Record{
				Event:          "control_command",
				ConversationID: env.ConversationID,
				SessionID:      sessionID,
				Sender:         env.Sender,
				MessageType:    "control",
				Decision:       "/agents switch",
			})
			return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, fmt.Sprintf("Swapped active agent order.\nPrimary model: %s\nFallback model: %s", normalizedPrimaryModel(&nextCfg), strings.TrimSpace(nextCfg.Agent.Fallback.Model)))
		default:
			return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Unknown agents command.")
		}
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

func (s *Service) handleReadOnlyControl(ctx context.Context, env types.IncomingEnvelope, sessionID string) error {
	command := firstField(effectiveIncomingText(env))
	_, err := s.handleReadOnlyControlMaybe(ctx, env, sessionID, command)
	return err
}

func (s *Service) handleReadOnlyControlMaybe(ctx context.Context, env types.IncomingEnvelope, sessionID, command string) (bool, error) {
	switch command {
	case "/help":
		s.audit(audit.Record{
			Event:          "control_command",
			ConversationID: env.ConversationID,
			SessionID:      sessionID,
			Sender:         env.Sender,
			MessageType:    "control",
			Decision:       "/help",
		})
		return true, s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, helpText())
	case "/version":
		s.audit(audit.Record{
			Event:          "control_command",
			ConversationID: env.ConversationID,
			SessionID:      sessionID,
			Sender:         env.Sender,
			MessageType:    "control",
			Decision:       "/version",
		})
		return true, s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, s.versionText())
	case "/config":
		fields := strings.Fields(strings.TrimSpace(effectiveIncomingText(env)))
		if len(fields) > 1 {
			return false, nil
		}
		s.audit(audit.Record{
			Event:          "control_command",
			ConversationID: env.ConversationID,
			SessionID:      sessionID,
			Sender:         env.Sender,
			MessageType:    "control",
			Decision:       "/config",
		})
		return true, s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, s.configText())
	case "/agents":
		fields := strings.Fields(strings.TrimSpace(effectiveIncomingText(env)))
		if len(fields) < 2 {
			return true, s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Usage: /agents <list|status|reload|switch|persona|format|thinking|fallback>")
		}
		switch fields[1] {
		case "list":
			s.audit(audit.Record{
				Event:          "control_command",
				ConversationID: env.ConversationID,
				SessionID:      sessionID,
				Sender:         env.Sender,
				MessageType:    "control",
				Decision:       "/agents list",
			})
			return true, s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, s.agentsListText())
		case "status":
			s.audit(audit.Record{
				Event:          "control_command",
				ConversationID: env.ConversationID,
				SessionID:      sessionID,
				Sender:         env.Sender,
				MessageType:    "control",
				Decision:       "/agents status",
			})
			return true, s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, s.agentsStatusText(ctx, env.ConversationID, sessionID))
		case "reload":
			if len(fields) > 2 {
				return false, nil
			}
			return true, s.handleAgentsReload(ctx, env, sessionID)
		case "persona":
			if len(fields) > 2 {
				return false, nil
			}
			s.audit(audit.Record{
				Event:          "control_command",
				ConversationID: env.ConversationID,
				SessionID:      sessionID,
				Sender:         env.Sender,
				MessageType:    "control",
				Decision:       "/agents persona",
			})
			persona, err := s.repo.GetSessionPersona(ctx, env.ConversationID, sessionID)
			if err != nil {
				return true, err
			}
			if strings.TrimSpace(persona) == "" {
				return true, s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "No persona is set for this session.")
			}
			return true, s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Current persona:\n"+strings.TrimSpace(persona))
		case "format":
			if len(fields) > 2 {
				return false, nil
			}
			s.audit(audit.Record{
				Event:          "control_command",
				ConversationID: env.ConversationID,
				SessionID:      sessionID,
				Sender:         env.Sender,
				MessageType:    "control",
				Decision:       "/agents format",
			})
			format, err := s.repo.GetSessionFormat(ctx, env.ConversationID, sessionID)
			if err != nil {
				return true, err
			}
			return true, s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Current format:\n"+effectiveSessionFormat(format))
		case "thinking":
			if len(fields) > 2 {
				return false, nil
			}
			if !thinkingConfiguredForAnyAgent(s.runtimeConfig()) {
				return true, s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Thinking control is not configured for the active agents.")
			}
			s.audit(audit.Record{
				Event:          "control_command",
				ConversationID: env.ConversationID,
				SessionID:      sessionID,
				Sender:         env.Sender,
				MessageType:    "control",
				Decision:       "/agents thinking",
			})
			mode, err := s.repo.GetSessionThinkingMode(ctx, env.ConversationID, sessionID)
			if err != nil {
				return true, err
			}
			return true, s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Current thinking mode:\n"+sessionThinkingModeText(s.runtimeConfig(), mode))
		case "fallback":
			if len(fields) > 2 {
				return false, nil
			}
			if !fallbackConfigured(s.config) {
				return true, s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Fallback agent is not configured.")
			}
			s.audit(audit.Record{
				Event:          "control_command",
				ConversationID: env.ConversationID,
				SessionID:      sessionID,
				Sender:         env.Sender,
				MessageType:    "control",
				Decision:       "/agents fallback",
			})
			disabled, err := s.repo.GetSessionFallbackDisabled(ctx, env.ConversationID, sessionID)
			if err != nil {
				return true, err
			}
			return true, s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Current fallback mode:\n"+sessionFallbackModeText(disabled))
		}
		return false, nil
	default:
		return false, nil
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

func firstField(text string) string {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
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

func (s *Service) versionText() string {
	text := "abx version " + strings.TrimSpace(s.version)
	if s.buildInfo == "" {
		return text
	}
	return text + "\nBuild: " + s.buildInfo
}

func helpText() string {
	return strings.Join([]string{
		"Available message types:",
		"- Normal questions: ask for explanations, summaries, brainstorming, or other chat help",
		"- /run <command-or-intent>: run an exact command or ask the agent to recommend one command for approval",
		"- YES <token>: approve the currently pending command in this chat",
		"",
		"Built-in commands:",
		"- /help: show this help message",
		"- /version: show version and build info",
		"- /config: show current configuration",
		"- /agents list: list configured agents",
		"- /agents status: show agent health status",
		"- /agents reload: reload agent config from disk",
		"- /agents persona: get/set persona for this session",
		"- /agents format: get/set response format for this session",
		"- /agents thinking: get/set thinking mode for this session",
		"- /agents fallback: get/set fallback agent status for this session",
		"- /agents switch: swap primary and fallback agents",
		"- /reset: start a new conversation session",
		"- /run: execute a shell command (with approval)",
	}, "\n")
}

func (s *Service) agentsListText() string {
	cfg := s.runtimeConfig()
	lines := []string{
		"Configured agents:",
		fmt.Sprintf("- primary: %s (%s)", normalizedPrimaryModel(cfg), normalizedContract(cfg.Agent.Primary.Provider)),
	}
	if fallbackConfigured(cfg) {
		lines = append(lines, fmt.Sprintf("- fallback: %s (%s)", strings.TrimSpace(cfg.Agent.Fallback.Model), normalizedContract(cfg.Agent.Fallback.Provider)))
	}
	return strings.Join(lines, "\n")
}

func (s *Service) chatWithSessionAgent(ctx context.Context, conversationID string, messages []types.Message, tools []types.Tool, fallbackDisabled bool, thinkingMode string) (types.AgentResponse, error) {
	runtimeAgent := s.runtimeAgent()
	options := types.AgentOptions{
		Thinking: thinkingModeOption(thinkingMode),
	}
	if fallbackDisabled {
		if primaryOnly, ok := runtimeAgent.(agent.PrimaryOnly); ok {
			return primaryOnly.ChatPrimaryWithOptions(ctx, messages, tools, options)
		}
	}
	if configurable, ok := runtimeAgent.(agent.OptionsProvider); ok {
		return configurable.ChatWithOptions(ctx, messages, tools, options)
	}
	return runtimeAgent.Chat(ctx, messages, tools)
}

func (s *Service) agentsStatusText(ctx context.Context, conversationID, sessionID string) string {
	cfg := s.runtimeConfig()
	lines := []string{
		"Agent status:",
		s.singleAgentStatusLine(ctx, "primary", cfg.Agent.Primary),
	}
	if fallbackConfigured(cfg) {
		lines = append(lines, s.singleAgentStatusLine(ctx, "fallback", cfg.Agent.Fallback))
		disabled, err := s.repo.GetSessionFallbackDisabled(ctx, conversationID, sessionID)
		if err == nil {
			lines = append(lines, "Fallback: "+sessionFallbackModeText(disabled))
		}
	}
	return strings.Join(lines, "\n")
}

func (s *Service) singleAgentStatusLine(ctx context.Context, label string, providerCfg config.ProviderConfig) string {
	model := strings.TrimSpace(providerCfg.Model)
	contract := normalizedContract(providerCfg.Provider)
	checker, ok := providerForStatus(providerCfg)
	if !ok {
		return fmt.Sprintf("- %s: %s (%s): unsupported", label, model, contract)
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := checker.Check(checkCtx); err != nil {
		s.logger.Printf("agent status check failed label=%s model=%s contract=%s err=%v", label, model, contract, err)
		return fmt.Sprintf("- %s: %s (%s): error", label, model, contract)
	}
	return fmt.Sprintf("- %s: %s (%s): ok", label, model, contract)
}

func providerForStatus(cfg config.ProviderConfig) (agent.Provider, bool) {
	switch normalizedContract(cfg.Provider) {
	case "openai-compatible":
		return openai.New(cfg), true
	default:
		return nil, false
	}
}

func (s *Service) configText() string {
	cfg := s.runtimeConfig()
	lines := []string{
		fmt.Sprintf("Messaging: %s / %s", normalizedMessagingProvider(cfg), normalizedRPCMode(cfg)),
		fmt.Sprintf("Primary model: %s", normalizedPrimaryModel(cfg)),
		fmt.Sprintf("Primary contract: %s", normalizedContract(cfg.Agent.Primary.Provider)),
		fmt.Sprintf("Primary request timeout: %ds", normalizedProviderRequestTimeout(cfg.Agent.Primary)),
	}
	if fallbackConfigured(cfg) {
		lines = append(lines,
			fmt.Sprintf("Fallback model: %s", strings.TrimSpace(cfg.Agent.Fallback.Model)),
			fmt.Sprintf("Fallback contract: %s", normalizedContract(cfg.Agent.Fallback.Provider)),
			fmt.Sprintf("Fallback request timeout: %ds", normalizedProviderRequestTimeout(cfg.Agent.Fallback)),
		)
	}
	lines = append(lines,
		fmt.Sprintf("MCP: %s", normalizedMCPStatus(cfg)),
		fmt.Sprintf("Storage: %s", normalizedDatabaseType(cfg)),
		fmt.Sprintf("Command policy: %s", normalizedPolicyMode(cfg)),
		fmt.Sprintf("Command timeout: %ds", normalizedCommandTimeout(cfg)),
		fmt.Sprintf("Thinking control: %s", normalizedThinkingState(cfg)),
		fmt.Sprintf("Debug: %s", normalizedDebugState(cfg)),
		fmt.Sprintf("Version: %s", strings.TrimSpace(s.version)),
	)
	return strings.Join(lines, "\n")
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

func normalizedProviderRequestTimeout(provider config.ProviderConfig) int {
	if provider.RequestTimeoutSeconds > 0 {
		return provider.RequestTimeoutSeconds
	}
	return 60
}

func thinkingModeOption(mode string) *bool {
	switch normalizeThinkingModeValue(mode) {
	case "enabled":
		value := true
		return &value
	case "disabled":
		value := false
		return &value
	default:
		return nil
	}
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

func prependPersonaMessage(messages []types.Message, persona string) []types.Message {
	persona = strings.TrimSpace(persona)
	out := make([]types.Message, 0, len(messages)+1)
	if persona == "" {
		out = append(out, types.Message{
			Role: types.RoleSystem,
			Text: "No custom persona is active for this session. Respond normally and do not continue any previously used persona unless the user asks for one.",
		})
		out = append(out, messages...)
		return out
	}
	out = append(out, types.Message{
		Role: types.RoleSystem,
		Text: "Persona:\n" + persona,
	})
	out = append(out, messages...)
	return out
}

func prependFormatMessage(messages []types.Message, format string) []types.Message {
	format = strings.TrimSpace(format)
	if format == "" {
		return append([]types.Message(nil), messages...)
	}
	out := make([]types.Message, 0, len(messages)+1)
	out = append(out, types.Message{
		Role: types.RoleSystem,
		Text: "Format:\n" + format,
	})
	out = append(out, messages...)
	return out
}

func prependConversationSystemPrompt(messages []types.Message) []types.Message {
	out := make([]types.Message, 0, len(messages)+1)
	out = append(out, types.Message{
		Role: types.RoleSystem,
		Text: "Reply to the latest user message directly and briefly when appropriate. Use the conversation context, but do not assume the user wants to execute commands, browse, fetch live data, use MCP tools, or start an approval flow unless they explicitly ask to run something or use `/run`. Do not invent approval tokens or command-execution instructions in normal conversation replies.",
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

func normalizePersona(text string) string {
	text = compactWhitespace(text)
	if len(text) > personaMaxChars {
		text = strings.TrimSpace(text[:personaMaxChars])
	}
	return text
}

func normalizeFormat(text string) string {
	text = compactWhitespace(text)
	if len(text) > formatMaxChars {
		text = strings.TrimSpace(text[:formatMaxChars])
	}
	return text
}

func effectiveSessionFormat(format string) string {
	format = strings.TrimSpace(format)
	if format == "" {
		return defaultSessionFormat
	}
	return format
}

func sessionFallbackModeText(disabled bool) string {
	if disabled {
		return "disabled for this session"
	}
	return "enabled for this session"
}

func sessionThinkingModeText(cfg *config.Config, mode string) string {
	switch normalizeThinkingModeValue(mode) {
	case "enabled":
		return "enabled for this session"
	case "disabled":
		return "disabled for this session"
	default:
		return "using agent default (" + defaultThinkingModeText(cfg) + ")"
	}
}

func (s *Service) formatAgentReply(response types.AgentResponse) string {
	text := response.Text
	cfg := s.runtimeConfig()
	if cfg == nil || !cfg.Debug.Enabled {
		return text
	}
	provider := strings.TrimSpace(response.Provider)
	if provider == "" {
		provider = strings.TrimSpace(cfg.Agent.Primary.Provider)
	}
	model := strings.TrimSpace(response.Model)
	if model == "" {
		model = strings.TrimSpace(cfg.Agent.Primary.Model)
	}
	class := strings.TrimSpace(response.EndpointClass)
	if class == "" {
		class = endpointClass(cfg.Agent.Primary.BaseURL)
	}
	details := []string{fmt.Sprintf("agent: %s / %s (%s)", provider, model, class)}
	if len(response.Integrations) > 0 {
		details = append(details, "mcp: "+strings.Join(response.Integrations, ", "))
	}
	stats := formatAgentStats(response)
	if stats != "" {
		details = append(details, stats)
	}
	label := "[" + strings.Join(details, " | ") + "]"
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return label
	}
	return trimmed + "\n\n" + label
}

func formatAgentStats(response types.AgentResponse) string {
	parts := make([]string, 0, 2)
	if response.TotalTokens > 0 || response.InputTokens > 0 || response.OutputTokens > 0 {
		switch {
		case response.InputTokens > 0 && response.OutputTokens > 0 && response.TotalTokens > 0:
			parts = append(parts, fmt.Sprintf("tokens: in=%d out=%d total=%d", response.InputTokens, response.OutputTokens, response.TotalTokens))
		case response.TotalTokens > 0:
			parts = append(parts, fmt.Sprintf("tokens: total=%d", response.TotalTokens))
		default:
			parts = append(parts, fmt.Sprintf("tokens: in=%d out=%d", response.InputTokens, response.OutputTokens))
		}
	}
	if response.TimeToFirst > 0 {
		parts = append(parts, fmt.Sprintf("ttft: %.2fs", response.TimeToFirst))
	}
	if len(parts) == 0 {
		return ""
	}
	return "stats: " + strings.Join(parts, " | ")
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

func (s *Service) runRecommendationMessages(ctx context.Context, conversationID, sessionID string) ([]types.Message, error) {
	history, err := s.repo.GetHistory(ctx, conversationID, sessionID, summaryHistoryLimit)
	if err != nil {
		return nil, err
	}
	history = normalizeHistoryForAgent(history)
	summary, err := s.repo.GetConversationSummary(ctx, conversationID, sessionID)
	if err != nil {
		return nil, err
	}
	olderHistory, recentHistory := splitHistoryForSummary(history, recentHistoryLimit)
	desiredSummary := summarizeMessages(olderHistory, summaryMaxChars)
	if desiredSummary != summary {
		if err := s.repo.SaveConversationSummary(ctx, conversationID, sessionID, desiredSummary); err != nil {
			return nil, err
		}
		summary = desiredSummary
	}
	messages := prependSummaryMessage(recentHistory, summary)
	// The agent must answer in a tight machine-readable format because the
	// recommendation parser only accepts COMMAND:/WHY: lines.
	instruction := types.Message{
		Role: types.RoleSystem,
		Text: "You are helping with `/run` in a local macOS shell environment. If the latest user message contains a shell command already, preserve it. Otherwise, recommend exactly one minimal bash command that best satisfies the user's intent. Prefer read-only commands when possible. Return exactly this format:\nCOMMAND: <single shell command>\nWHY: <one short sentence>\nIf you cannot recommend a command safely, return:\nCOMMAND:\nWHY: <brief reason>",
	}
	return append([]types.Message{instruction}, messages...), nil
}

func parseRunRecommendation(text string) (command, reason string, ok bool) {
	// Keep parsing deliberately strict so malformed model output cannot be
	// mistaken for an executable shell command recommendation.
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(strings.ToUpper(trimmed), "COMMAND:"):
			command = strings.TrimSpace(trimmed[len("COMMAND:"):])
		case strings.HasPrefix(strings.ToUpper(trimmed), "WHY:"):
			reason = strings.TrimSpace(trimmed[len("WHY:"):])
		}
	}
	command = compactWhitespace(command)
	reason = compactWhitespace(reason)
	if command == "" {
		return "", reason, false
	}
	return command, reason, true
}

func parseConversationCommandProposal(text string) (command, reason string, ok bool) {
	if command, reason, ok := parseRunRecommendation(text); ok {
		return command, reason, true
	}

	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	commandLine := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(trimmed), "COMMAND:") {
			commandLine = i
			inline := strings.TrimSpace(trimmed[len("COMMAND:"):])
			if inline != "" {
				command = inline
			}
			break
		}
	}
	if commandLine < 0 {
		return "", "", false
	}

	var reasonParts []string
	for _, line := range lines[:commandLine] {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			reasonParts = append(reasonParts, trimmed)
		}
	}
	if command == "" {
		for _, line := range lines[commandLine+1:] {
			trimmed := strings.TrimSpace(line)
			switch {
			case trimmed == "":
				if command != "" {
					goto done
				}
			case strings.HasPrefix(strings.ToUpper(trimmed), "REPLY WITH:"),
				strings.HasPrefix(strings.ToUpper(trimmed), "WHY:"),
				strings.HasPrefix(strings.ToUpper(trimmed), "YES "):
				goto done
			default:
				command = trimmed
			}
		}
	}

done:
	command = compactWhitespace(command)
	reason = compactWhitespace(strings.Join(reasonParts, " "))
	if command == "" {
		return "", reason, false
	}
	return command, reason, true
}

func formatCommandProposal(command, reason, nonce string) string {
	var b strings.Builder
	b.WriteString("Command:\n")
	b.WriteString(strings.TrimSpace(command))
	if strings.TrimSpace(reason) != "" {
		b.WriteString("\n\nWhy:\n")
		b.WriteString(strings.TrimSpace(reason))
	}
	b.WriteString("\n\nReply with:\nYES ")
	b.WriteString(strings.TrimSpace(nonce))
	return b.String()
}

func formatBlockedRecommendation(command, reason string, err error) string {
	var b strings.Builder
	b.WriteString("Command not allowed by the current policy.\n\n")
	b.WriteString("Suggested command:\n")
	b.WriteString(strings.TrimSpace(command))
	if strings.TrimSpace(reason) != "" {
		b.WriteString("\n\nWhy:\n")
		b.WriteString(strings.TrimSpace(reason))
	}
	if err != nil {
		b.WriteString("\n\nPolicy result:\n")
		b.WriteString(strings.TrimSpace(err.Error()))
	}
	return b.String()
}

func formatCommandResultMessage(output string, execErr error) (storedText, displayText string) {
	output = strings.TrimSpace(output)
	// Keep the full output for the user-facing reply, but store a bounded version
	// in history so recent command output does not overwhelm future agent context.
	contextOutput, truncated := truncateForAgentContext(output, commandContextChars)

	if execErr != nil {
		displayText = strings.TrimSpace(execErr.Error() + "\n" + output)
		storedText = strings.TrimSpace(execErr.Error() + "\n" + contextOutput)
		if truncated {
			storedText += "\n\n[command output truncated for conversation context]"
		}
		return "Command failed:\n" + storedText, "Command failed:\n" + displayText
	}

	displayText = strings.TrimSpace(output)
	storedText = contextOutput
	if truncated {
		storedText += "\n\n[command output truncated for conversation context]"
	}
	return "Command completed.\n" + strings.TrimSpace(storedText), "Command completed.\n" + strings.TrimSpace(displayText)
}

func truncateForAgentContext(text string, maxChars int) (string, bool) {
	text = strings.TrimSpace(text)
	if maxChars <= 0 || len(text) <= maxChars {
		return text, false
	}
	if maxChars <= 3 {
		return text[:maxChars], true
	}
	return text[:maxChars-3] + "...", true
}

func looksLikeExactCommand(input string) bool {
	// Bias toward treating shell-shaped input as an explicit command so `/run`
	// remains predictable and doesn't silently reinterpret commands as intents.
	input = strings.TrimSpace(input)
	if input == "" {
		return false
	}
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return false
	}
	if len(fields) == 1 {
		return true
	}
	if strings.ContainsAny(input, "|&;<>()$*?[]{}=`") {
		return true
	}
	for _, field := range fields[1:] {
		if strings.HasPrefix(field, "-") || strings.HasPrefix(field, "/") || strings.HasPrefix(field, "./") || strings.HasPrefix(field, "~/") || strings.Contains(field, "=") {
			return true
		}
	}
	switch strings.ToLower(fields[0]) {
	case "awk", "bash", "cat", "cd", "chmod", "chown", "cp", "curl", "date", "df", "du", "echo", "env",
		"find", "git", "grep", "head", "kill", "ls", "mkdir", "mv", "pwd", "ps", "python", "python3",
		"rm", "rmdir", "sed", "sh", "tail", "touch", "uname", "whoami":
		return true
	default:
		return false
	}
}

func effectiveIncomingText(env types.IncomingEnvelope) string {
	if text := strings.TrimSpace(env.NormalizedText); text != "" {
		return text
	}
	return strings.TrimSpace(env.Text)
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

func normalizedMessagingProvider(cfg *config.Config) string {
	if cfg == nil {
		return "signal-cli"
	}
	value := strings.TrimSpace(cfg.Messaging.Provider)
	if value == "" {
		return "signal-cli"
	}
	return value
}

func normalizedRPCMode(cfg *config.Config) string {
	if cfg == nil {
		return "json-rpc"
	}
	value := strings.TrimSpace(cfg.Messaging.SignalCLI.RPCMode)
	if value == "" {
		return "json-rpc"
	}
	return value
}

func normalizedPrimaryModel(cfg *config.Config) string {
	if cfg == nil {
		return "(unset)"
	}
	value := strings.TrimSpace(cfg.Agent.Primary.Model)
	if value == "" {
		return "(unset)"
	}
	return value
}

func normalizedContract(provider string) string {
	switch strings.TrimSpace(strings.ToLower(provider)) {
	case "", "openai":
		return "openai-compatible"
	default:
		return strings.TrimSpace(provider)
	}
}

func fallbackConfigured(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return strings.TrimSpace(cfg.Agent.Fallback.Provider) != ""
}

func normalizedMCPStatus(cfg *config.Config) string {
	if cfg == nil {
		return "disabled"
	}
	enabled := cfg.MCP.EnabledServerNames()
	if len(enabled) == 0 {
		return "disabled"
	}
	return "enabled (" + strings.Join(enabled, ", ") + ")"
}

func normalizedDatabaseType(cfg *config.Config) string {
	if cfg == nil {
		return "sqlite"
	}
	value := strings.TrimSpace(cfg.Database.Type)
	if value == "" {
		return "sqlite"
	}
	return value
}

func normalizedPolicyMode(cfg *config.Config) string {
	if cfg == nil {
		return "allowlist"
	}
	value := strings.TrimSpace(cfg.Command.PolicyMode)
	if value == "" {
		return "allowlist"
	}
	return value
}

func normalizedCommandTimeout(cfg *config.Config) int {
	if cfg == nil || cfg.Command.TimeoutSeconds <= 0 {
		return 60
	}
	return cfg.Command.TimeoutSeconds
}

func normalizedDebugState(cfg *config.Config) string {
	if cfg != nil && cfg.Debug.Enabled {
		return "enabled"
	}
	return "disabled"
}

func normalizedThinkingState(cfg *config.Config) string {
	if !thinkingConfiguredForAnyAgent(cfg) {
		return "not configured"
	}
	return "configured (default: " + defaultThinkingModeText(cfg) + ")"
}

func normalizeThinkingModeValue(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "enabled", "enable", "on", "true":
		return "enabled"
	case "disabled", "disable", "off", "false":
		return "disabled"
	default:
		return ""
	}
}

func thinkingConfiguredForAnyAgent(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return thinkingConfigured(cfg.Agent.Primary) || thinkingConfigured(cfg.Agent.Fallback)
}

func thinkingConfigured(provider config.ProviderConfig) bool {
	return strings.TrimSpace(provider.Thinking.ParameterPath) != "" ||
		strings.TrimSpace(provider.Thinking.EnableSuffix) != "" ||
		strings.TrimSpace(provider.Thinking.DisableSuffix) != "" ||
		strings.TrimSpace(provider.Thinking.DefaultMode) != ""
}

func defaultThinkingModeText(cfg *config.Config) string {
	if cfg == nil {
		return thinkingModeDefault
	}
	primary := normalizeThinkingModeValue(cfg.Agent.Primary.Thinking.DefaultMode)
	fallback := normalizeThinkingModeValue(cfg.Agent.Fallback.Thinking.DefaultMode)
	switch {
	case primary == "" && fallback == "":
		return thinkingModeDefault
	case fallback == "" || fallback == primary:
		return primary
	case primary == "":
		return fallback
	default:
		return primary + " primary / " + fallback + " fallback"
	}
}

func (s *Service) handleAgentsReload(ctx context.Context, env types.IncomingEnvelope, sessionID string) error {
	if s.reload == nil {
		return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Agent reload is not available in the current runtime.")
	}
	reloaded, err := s.reload(ctx)
	if err != nil {
		return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, fmt.Sprintf("Agent reload failed: %v", err))
	}
	if reloaded == nil || reloaded.Config == nil || reloaded.Agent == nil {
		return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Agent reload failed: runtime reload returned an incomplete agent configuration.")
	}
	_, _, runtimeExecutor := s.runtimeSnapshot()
	s.setRuntime(reloaded.Config, reloaded.Agent, runtimeExecutor)
	s.audit(audit.Record{
		Event:          "control_command",
		ConversationID: env.ConversationID,
		SessionID:      sessionID,
		Sender:         env.Sender,
		MessageType:    "control",
		Decision:       "/agents reload",
	})
	return s.sendAssistant(ctx, env.ConversationID, sessionID, env.ChatType, "Agents reloaded.\n"+s.configText())
}

func (s *Service) runtimeSnapshot() (*config.Config, agent.Provider, commandExecutor) {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return s.config, s.agent, s.executor
}

func (s *Service) runtimeConfig() *config.Config {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return s.config
}

func (s *Service) runtimeAgent() agent.Provider {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return s.agent
}

func (s *Service) runtimeExecutor() commandExecutor {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return s.executor
}

func (s *Service) setRuntime(cfg *config.Config, runtimeAgent agent.Provider, runtimeExecutor commandExecutor) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	s.config = cfg
	s.agent = runtimeAgent
	s.executor = runtimeExecutor
}
