package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// TestZeroClawSendAndReceive verifies the full send→done flow.
func TestZeroClawSendAndReceive(t *testing.T) {
	var upgrader = websocket.Upgrader{}
	receivedMsg := make(chan string, 1)
	done := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()

		// Read the client's message.
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read: %v", err)
			return
		}
		receivedMsg <- string(msg)

		// Send a chunk then a done frame.
		chunk := `{"type":"chunk","content":"Hello "}`
		_ = conn.WriteMessage(websocket.TextMessage, []byte(chunk))

		doneFrame := `{"type":"done","full_response":"Hello world"}`
		_ = conn.WriteMessage(websocket.TextMessage, []byte(doneFrame))

		<-done
	}))
	defer srv.Close()
	defer close(done)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	zc := &ZeroClaw{url: wsURL, token: "test-token"}

	if err := zc.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	defer func() { _ = zc.Close() }()

	reply, err := zc.SendAndReceive(context.Background(), &Request{
		Text:           "Hi",
		IdempotencyKey: "msg-1",
	})
	if err != nil {
		t.Fatalf("SendAndReceive() failed: %v", err)
	}

	if reply != "Hello world" {
		t.Errorf("expected reply %q, got %q", "Hello world", reply)
	}

	// Verify the message sent to the server.
	raw := <-receivedMsg
	var sent struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw), &sent); err != nil {
		t.Fatalf("unmarshal sent message: %v", err)
	}
	if sent.Type != "message" {
		t.Errorf("expected type 'message', got %q", sent.Type)
	}
	if sent.Content != "Hi" {
		t.Errorf("expected content 'Hi', got %q", sent.Content)
	}
}

// TestZeroClawErrorResponse verifies that an error frame returns an error.
func TestZeroClawErrorResponse(t *testing.T) {
	var upgrader = websocket.Upgrader{}
	done := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		_, _, _ = conn.ReadMessage()

		errFrame := `{"type":"error","message":"rate limit exceeded"}`
		_ = conn.WriteMessage(websocket.TextMessage, []byte(errFrame))

		<-done
	}))
	defer srv.Close()
	defer close(done)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	zc := &ZeroClaw{url: wsURL}

	if err := zc.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	defer func() { _ = zc.Close() }()

	_, err := zc.SendAndReceive(context.Background(), &Request{Text: "Hi"})
	if err == nil {
		t.Fatal("expected error from error frame")
	}
	if !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("error should contain server message, got: %v", err)
	}
}

// TestZeroClawConnectSendsAuthToken verifies the bearer token is sent in the
// Authorization header during the WebSocket handshake.
func TestZeroClawConnectSendsAuthToken(t *testing.T) {
	var upgrader = websocket.Upgrader{}
	gotToken := make(chan string, 1)
	done := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		gotToken <- auth

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		<-done
	}))
	defer srv.Close()
	defer close(done)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	wantToken := "my-secret-zeroclaw-token"
	zc := &ZeroClaw{url: wsURL, token: wantToken}

	if err := zc.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	defer func() { _ = zc.Close() }()

	token := <-gotToken
	expected := fmt.Sprintf("Bearer %s", wantToken)
	if token != expected {
		t.Errorf("expected Authorization %q, got %q", expected, token)
	}
}
