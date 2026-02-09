package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

// Auth holds Slack API tokens
type Auth struct {
	BotToken string
	AppToken string
}

// MessageEvent represents a thread reply received via Socket Mode
type MessageEvent struct {
	Platform  string // always "slack"
	ChannelID string
	ThreadTS  string
	UserID    string
	Username  string
	Text      string
	TS        string
}

// MessageHandler is called for each thread reply
type MessageHandler func(MessageEvent)

// Client is a minimal Slack Socket Mode client
type Client struct {
	auth      Auth
	userCache map[string]string
	userMu    sync.RWMutex
	logf      func(string, ...interface{})
}

// LoadAuth reads Slack credentials from ~/.secrets/slack/auth.json
func LoadAuth() (*Auth, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	path := filepath.Join(home, ".secrets", "slack", "auth.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read auth file: %w", err)
	}

	var raw struct {
		Bots map[string]struct {
			BotToken string `json:"bot_token"`
			AppToken string `json:"app_token"`
		} `json:"bots"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse auth file: %w", err)
	}

	bot, ok := raw.Bots["trevor-bot"]
	if !ok {
		return nil, fmt.Errorf("no trevor-bot config in auth file")
	}
	if bot.AppToken == "" {
		return nil, fmt.Errorf("no app_token for trevor-bot")
	}
	if bot.BotToken == "" {
		return nil, fmt.Errorf("no bot_token for trevor-bot")
	}

	return &Auth{BotToken: bot.BotToken, AppToken: bot.AppToken}, nil
}

// NewClient creates a Socket Mode client
func NewClient(auth Auth, logf func(string, ...interface{})) *Client {
	if logf == nil {
		logf = log.Printf
	}
	return &Client{
		auth:      auth,
		userCache: make(map[string]string),
		logf:      logf,
	}
}

// Listen connects to Slack Socket Mode and delivers thread reply events to handler.
// It reconnects automatically with exponential backoff. Blocks until ctx is cancelled.
func (c *Client) Listen(ctx context.Context, handler MessageHandler) error {
	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := c.listenOnce(ctx, handler)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		c.logf("[slack] connection lost: %v, reconnecting in %v", err, backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// connectionsOpen calls apps.connections.open to get a WebSocket URL
func (c *Client) connectionsOpen() (string, error) {
	req, err := http.NewRequest("POST", "https://slack.com/api/apps.connections.open", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.auth.AppToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("connections.open request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var result struct {
		OK  bool   `json:"ok"`
		URL string `json:"url"`
		Err string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if !result.OK {
		return "", fmt.Errorf("connections.open failed: %s", result.Err)
	}

	return result.URL, nil
}

// listenOnce connects and processes events until the connection drops
func (c *Client) listenOnce(ctx context.Context, handler MessageHandler) error {
	wsURL, err := c.connectionsOpen()
	if err != nil {
		return fmt.Errorf("get websocket url: %w", err)
	}

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}
	defer conn.CloseNow()

	// Set a generous read limit for large events
	conn.SetReadLimit(1 << 20) // 1MB

	c.logf("[slack] Socket Mode connected")

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("websocket read: %w", err)
		}

		// Parse envelope
		var envelope struct {
			EnvelopeID string `json:"envelope_id"`
			Type       string `json:"type"`
			Payload    struct {
				Event struct {
					Type     string `json:"type"`
					Subtype  string `json:"subtype"`
					Channel  string `json:"channel"`
					ThreadTS string `json:"thread_ts"`
					User     string `json:"user"`
					Text     string `json:"text"`
					TS       string `json:"ts"`
					BotID    string `json:"bot_id"`
				} `json:"event"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			c.logf("[slack] parse envelope error: %v", err)
			continue
		}

		// Acknowledge the envelope immediately
		if envelope.EnvelopeID != "" {
			ack, _ := json.Marshal(map[string]string{"envelope_id": envelope.EnvelopeID})
			if err := conn.Write(ctx, websocket.MessageText, ack); err != nil {
				return fmt.Errorf("ack write: %w", err)
			}
		}

		// Only process events_api envelopes with message events
		if envelope.Type != "events_api" {
			continue
		}
		event := envelope.Payload.Event
		if event.Type != "message" {
			continue
		}

		// Skip bot messages, edits, deletes
		if event.Subtype != "" {
			continue
		}
		// Skip if no thread_ts (not a thread reply)
		if event.ThreadTS == "" {
			continue
		}
		// Skip parent messages (thread_ts == ts means it's the thread starter)
		if event.ThreadTS == event.TS {
			continue
		}
		// Bot messages pass through - daemon filters by session [xxxx] prefix instead

		// Resolve username
		username := c.resolveUsername(event.User)

		handler(MessageEvent{
			Platform:  "slack",
			ChannelID: event.Channel,
			ThreadTS:  event.ThreadTS,
			UserID:    event.User,
			Username:  username,
			Text:      event.Text,
			TS:        event.TS,
		})
	}
}

// resolveUsername looks up a user's display name, with caching
func (c *Client) resolveUsername(userID string) string {
	if userID == "" {
		return "unknown"
	}

	c.userMu.RLock()
	if name, ok := c.userCache[userID]; ok {
		c.userMu.RUnlock()
		return name
	}
	c.userMu.RUnlock()

	// Call users.info API
	req, err := http.NewRequest("GET", fmt.Sprintf("https://slack.com/api/users.info?user=%s", userID), nil)
	if err != nil {
		return userID
	}
	req.Header.Set("Authorization", "Bearer "+c.auth.BotToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return userID
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return userID
	}

	var result struct {
		OK   bool `json:"ok"`
		User struct {
			RealName string `json:"real_name"`
			Name     string `json:"name"`
			Profile  struct {
				DisplayName string `json:"display_name"`
			} `json:"profile"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &result); err != nil || !result.OK {
		return userID
	}

	// Prefer display_name > real_name > name
	name := result.User.Profile.DisplayName
	if name == "" {
		name = result.User.RealName
	}
	if name == "" {
		name = result.User.Name
	}
	if name == "" {
		name = userID
	}

	c.userMu.Lock()
	c.userCache[userID] = name
	c.userMu.Unlock()

	return name
}
