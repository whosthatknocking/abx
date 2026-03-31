package signalcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/whosthatknocking/abx/internal/config"
	"github.com/whosthatknocking/abx/pkg/types"
)

type Adapter struct {
	cfg    config.SignalCLIConfig
	logger *log.Logger
	mu     sync.Mutex
}

func New(cfg config.SignalCLIConfig, logger *log.Logger) *Adapter {
	return &Adapter{cfg: cfg, logger: logger}
}

func (a *Adapter) Start(ctx context.Context, handler func(types.IncomingEnvelope)) error {
	conn, err := a.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	a.logger.Printf("signal-cli adapter connected rpc_mode=%s account=%s", a.cfg.RPCMode, a.cfg.Account)
	_, _ = a.sendRPC(conn, "subscribe", map[string]any{
		"account": a.cfg.Account,
	})

	reader := bufio.NewReader(conn)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("read signal-cli event: %w", err)
		}
		env, ok := decodeEnvelope(a.cfg.Account, line)
		if !ok {
			continue
		}
		handler(env)
	}
}

func (a *Adapter) Send(ctx context.Context, recipient, text string) error {
	conn, err := a.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	params, err := sendParams(a.cfg.Account, recipient, text)
	if err != nil {
		return err
	}
	_, err = a.sendRPC(conn, "send", params)
	return err
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

func (a *Adapter) sendRPC(conn net.Conn, method string, params map[string]any) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	id := strconv.FormatInt(time.Now().UnixNano(), 10)
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	raw = append(raw, '\n')
	if _, err := conn.Write(raw); err != nil {
		return "", fmt.Errorf("write signal-cli request: %w", err)
	}
	return id, nil
}

func decodeEnvelope(account string, raw []byte) (types.IncomingEnvelope, bool) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return types.IncomingEnvelope{}, false
	}
	event := childMap(payload, "params")
	if len(event) == 0 {
		event = payload
	}
	envelope := childMap(event, "envelope")
	if len(envelope) == 0 {
		return types.IncomingEnvelope{}, false
	}
	dataMessage := childMap(envelope, "dataMessage")
	messageText := stringField(dataMessage, "message")
	if messageText == "" {
		messageText = stringField(envelope, "message")
	}
	groupInfo := childMap(dataMessage, "groupInfo")
	chatType := types.ChatTypeDirect
	conversationID := "direct:" + stringField(envelope, "sourceNumber")
	if len(groupInfo) > 0 {
		chatType = types.ChatTypeGroup
		if groupID := stringField(groupInfo, "groupId"); groupID != "" {
			conversationID = "group:" + groupID
		}
	}
	if messageText == "" || conversationID == "" {
		return types.IncomingEnvelope{}, false
	}
	return types.IncomingEnvelope{
		ID:             stringField(envelope, "timestamp"),
		ConversationID: conversationID,
		Sender:         stringField(envelope, "sourceNumber"),
		Recipient:      account,
		ChatType:       chatType,
		Text:           strings.TrimSpace(messageText),
		MentionedBot:   mentionedBot(dataMessage, account),
		CreatedAt:      time.Now(),
	}, true
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
		params["recipients"] = []string{recipient}
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
		params["recipients"] = []string{target}
	}
	return params, nil
}
