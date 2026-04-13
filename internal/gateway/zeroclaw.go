package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Enriquefft/openclaw-kapso-whatsapp/internal/config"
	"github.com/gorilla/websocket"
)

// ZeroClaw implements Gateway for the ZeroClaw agent runtime.
// It communicates via WebSocket at /ws/chat with streaming responses.
type ZeroClaw struct {
	url   string
	token string

	conn *websocket.Conn
	mu   sync.Mutex
}

// NewZeroClaw creates a ZeroClaw gateway from config.
func NewZeroClaw(cfg config.GatewayConfig) *ZeroClaw {
	return &ZeroClaw{
		url:   cfg.URL,
		token: cfg.Token,
	}
}

// Connect establishes a WebSocket connection to ZeroClaw's /ws/chat endpoint.
func (zc *ZeroClaw) Connect(ctx context.Context) error {
	zc.mu.Lock()
	defer zc.mu.Unlock()

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	// Auth via Authorization header.
	headers := http.Header{}
	if zc.token != "" {
		headers.Set("Authorization", "Bearer "+zc.token)
	}

	conn, _, err := dialer.DialContext(ctx, zc.url, headers)
	if err != nil {
		return fmt.Errorf("connect to zeroclaw: %w", err)
	}
	zc.conn = conn

	log.Printf("connected to zeroclaw at %s", zc.url)
	return nil
}

// SendAndReceive sends a message to ZeroClaw and waits for the full response.
// ZeroClaw streams chunks and finishes with a "done" frame containing the
// complete response text.
func (zc *ZeroClaw) SendAndReceive(ctx context.Context, req *Request) (string, error) {
	zc.mu.Lock()
	if zc.conn == nil {
		zc.mu.Unlock()
		return "", fmt.Errorf("not connected to zeroclaw")
	}
	conn := zc.conn
	zc.mu.Unlock()

	// Send message — ZeroClaw takes raw text content.
	msg := map[string]string{
		"type":    "message",
		"content": req.Text,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("marshal message: %w", err)
	}

	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return "", fmt.Errorf("write message: %w", err)
	}

	// Read frames until we get a "done" or "error" response.
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		_, raw, err := conn.ReadMessage()
		if err != nil {
			return "", fmt.Errorf("read response: %w", err)
		}

		var frame struct {
			Type         string `json:"type"`
			Content      string `json:"content"`
			FullResponse string `json:"full_response"`
			Message      string `json:"message"`
		}
		if err := json.Unmarshal(raw, &frame); err != nil {
			log.Printf("zeroclaw: ignoring unparseable frame: %s", string(raw))
			continue
		}

		switch frame.Type {
		case "done":
			return frame.FullResponse, nil
		case "error":
			return "", fmt.Errorf("zeroclaw agent error: %s", frame.Message)
		case "chunk", "tool_call", "tool_result":
			// Streaming progress — continue reading.
			continue
		default:
			log.Printf("zeroclaw: unknown frame type %q", frame.Type)
			continue
		}
	}
}

// Close closes the WebSocket connection.
func (zc *ZeroClaw) Close() error {
	zc.mu.Lock()
	defer zc.mu.Unlock()

	if zc.conn != nil {
		return zc.conn.Close()
	}
	return nil
}
