package signalcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/whosthatknocking/abx/internal/config"
	"github.com/whosthatknocking/abx/pkg/types"
)

type Adapter struct {
	cfg    config.SignalCLIConfig
	logger *log.Logger
}

const (
	reconnectDelay  = 2 * time.Second
	rpcTimeout      = 15 * time.Second
	maxMessageRunes = 3000
)

type rpcMessage struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Method  string         `json:"method,omitempty"`
	Params  map[string]any `json:"params,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
	Error   *rpcError      `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

func New(cfg config.SignalCLIConfig, logger *log.Logger) *Adapter {
	if logger == nil {
		logger = log.Default()
	}
	return &Adapter{cfg: cfg, logger: logger}
}

func (a *Adapter) Start(ctx context.Context, handler func(types.IncomingEnvelope)) error {
	for {
		err := a.runSession(ctx, handler)
		if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ctx.Err()
		}
		a.logger.Printf("signal-cli adapter disconnected err=%v reconnect_in=%s", err, reconnectDelay)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(reconnectDelay):
		}
	}
}

func (a *Adapter) Send(ctx context.Context, recipient, text string) error {
	conn, err := a.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	chunks := splitOutgoingMessage(text, maxMessageRunes)
	for i, chunk := range chunks {
		params, err := sendParams(a.cfg.Account, recipient, chunk)
		if err != nil {
			return err
		}
		if _, err := a.sendRPC(ctx, conn, reader, "send", params); err != nil {
			return err
		}
		if len(chunks) > 1 {
			a.logger.Printf("signal-cli adapter sent chunk %d/%d recipient=%s chars=%d", i+1, len(chunks), recipient, len([]rune(chunk)))
		}
	}
	return nil
}

func (a *Adapter) runSession(ctx context.Context, handler func(types.IncomingEnvelope)) error {
	conn, err := a.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	reader := bufio.NewReader(conn)
	a.logger.Printf("signal-cli adapter connected rpc_mode=%s account=%s", a.cfg.RPCMode, a.cfg.Account)
	if _, err := a.sendRPC(ctx, conn, reader, "subscribe", map[string]any{
		"account": a.cfg.Account,
	}); err != nil {
		if isMethodNotImplementedError(err) {
			a.logger.Printf("signal-cli subscribe unsupported; continuing without explicit subscription")
		} else {
			return fmt.Errorf("subscribe to signal-cli events: %w", err)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msg, err := readRPCMessage(conn, reader, 2*time.Second)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
				return fmt.Errorf("signal-cli connection closed: %w", err)
			}
			return fmt.Errorf("read signal-cli event: %w", err)
		}
		if msg.Method != "receive" {
			if msg.Error != nil {
				a.logger.Printf("signal-cli rpc error code=%d message=%s", msg.Error.Code, msg.Error.Message)
			}
			continue
		}
		env, ok := decodeEnvelopeFromRPC(a.cfg.Account, msg)
		if !ok {
			continue
		}
		handler(env)
	}
}

func (a *Adapter) dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	switch {
	case a.cfg.RPCSocket != "":
		return d.DialContext(ctx, "unix", a.cfg.RPCSocket)
	case a.cfg.RPCHost != "" && a.cfg.RPCPort > 0:
		return d.DialContext(ctx, "tcp", net.JoinHostPort(a.cfg.RPCHost, strconv.Itoa(a.cfg.RPCPort)))
	default:
		return nil, fmt.Errorf("signal-cli rpc endpoint is not configured")
	}
}

func (a *Adapter) sendRPC(ctx context.Context, conn net.Conn, reader *bufio.Reader, method string, params map[string]any) (map[string]any, error) {
	id := strconv.FormatInt(time.Now().UnixNano(), 10)
	payload := rpcMessage{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if _, err := conn.Write(raw); err != nil {
		return nil, fmt.Errorf("write signal-cli request: %w", err)
	}
	return awaitRPCResponse(ctx, conn, reader, id)
}

func decodeEnvelopeFromRPC(account string, payload rpcMessage) (types.IncomingEnvelope, bool) {
	event := payload.Params
	envelope := childMap(event, "envelope")
	if len(envelope) == 0 {
		return types.IncomingEnvelope{}, false
	}
	dataMessage := childMap(envelope, "dataMessage")
	messageText := stringField(dataMessage, "message")
	if messageText == "" {
		messageText = stringField(envelope, "message")
	}
	attachments := imageAttachments(dataMessage)
	groupInfo := childMap(dataMessage, "groupInfo")
	chatType := types.ChatTypeDirect
	conversationID := "direct:" + stringField(envelope, "sourceNumber")
	if len(groupInfo) > 0 {
		chatType = types.ChatTypeGroup
		if groupID := stringField(groupInfo, "groupId"); groupID != "" {
			conversationID = "group:" + groupID
		}
	}
	if (messageText == "" && len(attachments) == 0) || conversationID == "" {
		return types.IncomingEnvelope{}, false
	}
	messageText = strings.TrimSpace(messageText)
	isMentioned := mentionedBot(dataMessage, account)
	return types.IncomingEnvelope{
		ID:             stringField(envelope, "timestamp"),
		ConversationID: conversationID,
		Sender:         stringField(envelope, "sourceNumber"),
		Recipient:      account,
		ChatType:       chatType,
		Text:           messageText,
		Attachments:    attachments,
		NormalizedText: normalizeGroupRoutingText(messageText, chatType, isMentioned),
		MentionedBot:   isMentioned,
		CreatedAt:      envelopeTimestamp(envelope),
	}, true
}

func imageAttachments(dataMessage map[string]any) []types.Attachment {
	items, ok := dataMessage["attachments"].([]any)
	if !ok {
		return nil
	}
	out := make([]types.Attachment, 0, len(items))
	for _, item := range items {
		attachment, ok := item.(map[string]any)
		if !ok {
			continue
		}
		contentType := strings.TrimSpace(stringField(attachment, "contentType"))
		if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
			continue
		}
		filePath := firstNonEmptyString(
			stringField(attachment, "storedFilename"),
			stringField(attachment, "storedPlaintext"),
			stringField(attachment, "storedPlaintextPath"),
			stringField(attachment, "storedPlaintextFile"),
			stringField(attachment, "file"),
			stringField(attachment, "path"),
		)
		if filePath == "" {
			continue
		}
		out = append(out, types.Attachment{
			Kind:        "image",
			ContentType: contentType,
			FilePath:    filePath,
			FileName:    firstNonEmptyString(stringField(attachment, "filename"), stringField(attachment, "storedFilename")),
		})
	}
	return out
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func normalizeGroupRoutingText(text string, chatType types.ChatType, mentionedBot bool) string {
	text = strings.TrimSpace(text)
	if chatType != types.ChatTypeGroup || !mentionedBot {
		return text
	}
	text = stripLeadingMentions(text)
	if approvalText, ok := extractApprovalText(text); ok {
		return approvalText
	}
	if commandText, ok := extractLeadingSlashCommand(text); ok {
		return commandText
	}
	return text
}

func stripLeadingMentions(text string) string {
	text = strings.TrimSpace(text)
	for strings.HasPrefix(text, "@") {
		fields := strings.Fields(text)
		if len(fields) == 0 {
			return ""
		}
		first := strings.TrimSpace(fields[0])
		if !strings.HasPrefix(first, "@") {
			break
		}
		text = strings.TrimSpace(strings.TrimPrefix(text, first))
		text = strings.TrimLeft(text, " \t,:;-")
	}
	return strings.TrimSpace(text)
}

func extractLeadingSlashCommand(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if idx := strings.Index(text, "/"); idx >= 0 {
		prefix := strings.TrimSpace(text[:idx])
		if prefix == "" || !strings.ContainsAny(prefix, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") {
			return strings.TrimSpace(text[idx:]), true
		}
	}
	return "", false
}

func extractApprovalText(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if idx := strings.Index(text, "YES "); idx >= 0 {
		prefix := strings.TrimSpace(text[:idx])
		if prefix == "" || !strings.ContainsAny(prefix, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") {
			return strings.TrimSpace(text[idx:]), true
		}
	}
	return "", false
}

func mentionedBot(dataMessage map[string]any, account string) bool {
	mentions, ok := dataMessage["mentions"].([]any)
	if !ok || len(mentions) == 0 {
		return false
	}
	for _, item := range mentions {
		mention, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"number", "recipient", "address", "uuid"} {
			if stringField(mention, key) == account {
				return true
			}
		}
	}
	return false
}

func childMap(m map[string]any, key string) map[string]any {
	v, ok := m[key]
	if !ok {
		return nil
	}
	mv, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return mv
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	switch vv := v.(type) {
	case string:
		return vv
	case float64:
		return strconv.FormatInt(int64(vv), 10)
	default:
		return fmt.Sprint(vv)
	}
}

func sendParams(account, target, text string) (map[string]any, error) {
	params := map[string]any{
		"account": account,
		"message": text,
	}
	switch {
	case strings.HasPrefix(target, "direct:"):
		recipient := strings.TrimSpace(strings.TrimPrefix(target, "direct:"))
		if recipient == "" {
			return nil, fmt.Errorf("signal-cli direct recipient is empty")
		}
		params["recipient"] = []string{recipient}
	case strings.HasPrefix(target, "group:"):
		groupID := strings.TrimSpace(strings.TrimPrefix(target, "group:"))
		if groupID == "" {
			return nil, fmt.Errorf("signal-cli group recipient is empty")
		}
		params["groupId"] = groupID
	default:
		if strings.TrimSpace(target) == "" {
			return nil, fmt.Errorf("signal-cli recipient is empty")
		}
		params["recipient"] = []string{target}
	}
	return params, nil
}

func awaitRPCResponse(ctx context.Context, conn net.Conn, reader *bufio.Reader, id string) (map[string]any, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		msg, err := readRPCMessage(conn, reader, rpcTimeout)
		if err != nil {
			return nil, err
		}
		if msg.Method == "receive" {
			continue
		}
		if normalizeRPCID(msg.ID) != id {
			continue
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("signal-cli rpc error %d: %s", msg.Error.Code, msg.Error.Message)
		}
		return msg.Result, nil
	}
}

func readRPCMessage(conn net.Conn, reader *bufio.Reader, timeout time.Duration) (rpcMessage, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return rpcMessage{}, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return rpcMessage{}, fmt.Errorf("decode signal-cli rpc message: %w", err)
	}
	return msg, nil
}

func normalizeRPCID(v any) string {
	switch vv := v.(type) {
	case string:
		return vv
	case float64:
		return strconv.FormatInt(int64(vv), 10)
	case nil:
		return ""
	default:
		return fmt.Sprint(vv)
	}
}

func envelopeTimestamp(envelope map[string]any) time.Time {
	ts := stringField(envelope, "timestamp")
	if ts == "" {
		return time.Now()
	}
	ms, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return time.Now()
	}
	return time.UnixMilli(ms)
}

func isMethodNotImplementedError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "method not implemented")
}

func splitOutgoingMessage(text string, maxRunes int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{""}
	}
	if maxRunes <= 0 || len([]rune(text)) <= maxRunes {
		return []string{text}
	}

	var chunks []string
	remaining := text
	for len([]rune(remaining)) > maxRunes {
		head, tail := splitChunk(remaining, maxRunes)
		chunks = append(chunks, head)
		remaining = strings.TrimSpace(tail)
	}
	if remaining != "" {
		chunks = append(chunks, remaining)
	}
	return chunks
}

func splitChunk(text string, maxRunes int) (string, string) {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return strings.TrimSpace(text), ""
	}

	cut := maxRunes
	for _, marker := range []string{"\n\n", "\n", " "} {
		if idx := lastIndexWithinRunes(text, marker, maxRunes); idx > 0 {
			cut = idx
			break
		}
	}
	head := strings.TrimSpace(string(runes[:cut]))
	tail := strings.TrimSpace(string(runes[cut:]))
	if head == "" {
		head = strings.TrimSpace(string(runes[:maxRunes]))
		tail = strings.TrimSpace(string(runes[maxRunes:]))
	}
	return head, tail
}

func lastIndexWithinRunes(text, marker string, maxRunes int) int {
	runes := []rune(text)
	if len(runes) == 0 || maxRunes <= 0 {
		return -1
	}
	limit := maxRunes
	if limit > len(runes) {
		limit = len(runes)
	}
	prefix := string(runes[:limit])
	byteIdx := strings.LastIndex(prefix, marker)
	if byteIdx < 0 {
		return -1
	}
	return len([]rune(prefix[:byteIdx]))
}
