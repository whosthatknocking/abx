package types

import "time"

type ChatType string

const (
	ChatTypeDirect ChatType = "direct"
	ChatTypeGroup  ChatType = "group"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type MessageKind string

const (
	MessageKindInbound  MessageKind = "inbound"
	MessageKindOutbound MessageKind = "outbound"
)

type Message struct {
	ID             string
	ConversationID string
	SessionID      string
	Sender         string
	Recipient      string
	Role           Role
	Kind           MessageKind
	ChatType       ChatType
	Text           string
	MentionedBot   bool
	CreatedAt      time.Time
}

type Tool struct {
	Name string
}

type AgentOptions struct {
	Thinking *bool
}

type AgentResponse struct {
	Text          string
	Provider      string
	Model         string
	EndpointClass string
	Integrations  []string
	ToolSummaries []string
	InputTokens   int
	OutputTokens  int
	TotalTokens   int
	TimeToFirst   float64
}

type PendingApproval struct {
	RequestID      string
	ConversationID string
	SessionID      string
	Command        string
	ProposedBy     string
	Nonce          string
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

type IncomingEnvelope struct {
	ID             string
	ConversationID string
	Sender         string
	Recipient      string
	ChatType       ChatType
	Text           string
	NormalizedText string
	MentionedBot   bool
	CreatedAt      time.Time
}
