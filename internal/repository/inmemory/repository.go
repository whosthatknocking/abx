package inmemory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	"github.com/whosthatknocking/abx/internal/repository"
	"github.com/whosthatknocking/abx/pkg/types"
)

type Repository struct {
	mu            sync.RWMutex
	conversations map[string]*conversationState
}

type conversationState struct {
	ActiveSessionID        string
	Sessions               map[string]*sessionState
	PendingByRequestID     map[string]types.PendingApproval
	ActivePendingRequestID string
}

type sessionState struct {
	Messages []types.Message
	Summary  string
}

func New() repository.Repository {
	return &Repository{
		conversations: map[string]*conversationState{},
	}
}

func (r *Repository) SaveMessage(_ context.Context, conversationID, sessionID string, msg types.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	conv := r.ensureConversation(conversationID)
	session := r.ensureSession(conv, sessionID)
	session.Messages = append(session.Messages, msg)
	return nil
}

func (r *Repository) GetActiveSessionID(_ context.Context, conversationID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	conv := r.ensureConversation(conversationID)
	return conv.ActiveSessionID, nil
}

func (r *Repository) GetHistory(_ context.Context, conversationID, sessionID string, limit int) ([]types.Message, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conv, ok := r.conversations[conversationID]
	if !ok {
		return nil, nil
	}
	session, ok := conv.Sessions[sessionID]
	if !ok {
		return nil, nil
	}
	return tailMessages(session.Messages, limit), nil
}

func (r *Repository) GetActiveHistory(ctx context.Context, conversationID string, limit int) ([]types.Message, error) {
	sessionID, err := r.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	return r.GetHistory(ctx, conversationID, sessionID, limit)
}

func (r *Repository) SaveConversationSummary(_ context.Context, conversationID, sessionID, summary string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	conv := r.ensureConversation(conversationID)
	session := r.ensureSession(conv, sessionID)
	session.Summary = summary
	return nil
}

func (r *Repository) GetConversationSummary(_ context.Context, conversationID, sessionID string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conv, ok := r.conversations[conversationID]
	if !ok {
		return "", nil
	}
	session, ok := conv.Sessions[sessionID]
	if !ok {
		return "", nil
	}
	return session.Summary, nil
}

func (r *Repository) GetActiveConversationSummary(ctx context.Context, conversationID string) (string, error) {
	sessionID, err := r.GetActiveSessionID(ctx, conversationID)
	if err != nil {
		return "", err
	}
	return r.GetConversationSummary(ctx, conversationID, sessionID)
}

func (r *Repository) RotateConversationSession(_ context.Context, conversationID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	conv := r.ensureConversation(conversationID)
	sessionID, err := newID()
	if err != nil {
		return "", err
	}
	conv.ActiveSessionID = sessionID
	conv.Sessions[sessionID] = &sessionState{}
	conv.ActivePendingRequestID = ""
	return sessionID, nil
}

func (r *Repository) SavePendingApproval(_ context.Context, conversationID, sessionID string, approval types.PendingApproval) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	conv := r.ensureConversation(conversationID)
	if conv.ActiveSessionID != sessionID {
		conv.ActiveSessionID = sessionID
	}
	conv.PendingByRequestID[approval.RequestID] = approval
	conv.ActivePendingRequestID = approval.RequestID
	return nil
}

func (r *Repository) GetPendingApproval(_ context.Context, conversationID, requestID string) (*types.PendingApproval, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conv, ok := r.conversations[conversationID]
	if !ok {
		return nil, nil
	}
	approval, ok := conv.PendingByRequestID[requestID]
	if !ok {
		return nil, nil
	}
	copy := approval
	return &copy, nil
}

func (r *Repository) GetActivePendingApproval(_ context.Context, conversationID string) (*types.PendingApproval, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conv, ok := r.conversations[conversationID]
	if !ok || conv.ActivePendingRequestID == "" {
		return nil, nil
	}
	approval, ok := conv.PendingByRequestID[conv.ActivePendingRequestID]
	if !ok {
		return nil, nil
	}
	copy := approval
	return &copy, nil
}

func (r *Repository) ClearPendingApproval(_ context.Context, conversationID, requestID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	conv, ok := r.conversations[conversationID]
	if !ok {
		return nil
	}
	delete(conv.PendingByRequestID, requestID)
	if conv.ActivePendingRequestID == requestID {
		conv.ActivePendingRequestID = ""
	}
	return nil
}

func (r *Repository) ensureConversation(conversationID string) *conversationState {
	conv, ok := r.conversations[conversationID]
	if ok {
		return conv
	}
	sessionID, err := newID()
	if err != nil {
		panic(fmt.Sprintf("generate session id: %v", err))
	}
	conv = &conversationState{
		ActiveSessionID:        sessionID,
		Sessions:               map[string]*sessionState{sessionID: {}},
		PendingByRequestID:     map[string]types.PendingApproval{},
		ActivePendingRequestID: "",
	}
	r.conversations[conversationID] = conv
	return conv
}

func (r *Repository) ensureSession(conv *conversationState, sessionID string) *sessionState {
	if sessionID == "" {
		sessionID = conv.ActiveSessionID
	}
	if conv.ActiveSessionID == "" {
		conv.ActiveSessionID = sessionID
	}
	session, ok := conv.Sessions[sessionID]
	if ok {
		return session
	}
	session = &sessionState{}
	conv.Sessions[sessionID] = session
	return session
}

func tailMessages(messages []types.Message, limit int) []types.Message {
	if limit <= 0 || len(messages) <= limit {
		out := append([]types.Message(nil), messages...)
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		})
		return out
	}
	out := append([]types.Message(nil), messages[len(messages)-limit:]...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func newID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
