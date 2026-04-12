package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// TestConnectRequestsReadAndWriteScopes verifies that the gateway connect
// handshake includes both "operator.read" and "operator.write" scopes.
// This is a regression test — the bridge previously sent "operator.admin"
// which the gateway does not recognise, silently blocking chat.send calls.
// As Blackadder would say: "A plan so cunning you could pin a tail on it
// and call it a weasel."
func TestConnectRequestsReadAndWriteScopes(t *testing.T) {
	var upgrader = websocket.Upgrader{}

	// Channel to capture the raw connect frame the client sends.
	connectFrame := make(chan []byte, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		// Send a fake challenge — the client expects to read one frame first.
		challenge := ResponseFrame{
			Type:   "event",
			Method: "challenge",
		}
		challengeData, _ := json.Marshal(challenge)
		if err := conn.WriteMessage(websocket.TextMessage, challengeData); err != nil {
			t.Fatalf("write challenge: %v", err)
			return
		}

		// Read the connect request from the client.
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read connect: %v", err)
			return
		}
		connectFrame <- msg

		// Send a success response so Connect() returns nil.
		resp := ResponseFrame{
			Type:   "res",
			ID:     "kapso-1",
			Result: json.RawMessage(`{"ok": true}`),
		}
		respData, _ := json.Marshal(resp)
		if err := conn.WriteMessage(websocket.TextMessage, respData); err != nil {
			t.Fatalf("write response: %v", err)
			return
		}

		// Keep connection open briefly so drain() goroutine doesn't log errors
		// before the test finishes. This is the WebSocket equivalent of
		// holding the door open while someone's still talking — polite, innit.
		<-make(chan struct{})
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	client := NewClient(wsURL, "test-token-nobody-expects-the-spanish-inquisition")

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	defer client.Close()

	// Parse the captured connect frame.
	raw := <-connectFrame

	var frame struct {
		Type   string `json:"type"`
		Method string `json:"method"`
		Params struct {
			Role   string   `json:"role"`
			Scopes []string `json:"scopes"`
			Auth   struct {
				Token string `json:"token"`
			} `json:"auth"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("unmarshal connect frame: %v", err)
	}

	if frame.Method != "connect" {
		t.Fatalf("expected method 'connect', got %q", frame.Method)
	}

	if frame.Params.Role != "operator" {
		t.Fatalf("expected role 'operator', got %q", frame.Params.Role)
	}

	// The money shot — this is the actual regression check.
	// "I've got it! We'll build a gateway that requires BOTH scopes!"
	// "That's what we already have, sir." — Baldrick
	scopes := frame.Params.Scopes
	hasRead := false
	hasWrite := false
	for _, s := range scopes {
		switch s {
		case "operator.read":
			hasRead = true
		case "operator.write":
			hasWrite = true
		case "operator.admin":
			t.Errorf("scope 'operator.admin' is not a valid gateway scope — this was the original bug")
		}
	}

	if !hasRead {
		t.Errorf("missing required scope 'operator.read'; got scopes: %v", scopes)
	}
	if !hasWrite {
		t.Errorf("missing required scope 'operator.write'; got scopes: %v — "+
			"without this the gateway blocks chat.send and your WhatsApp messages "+
			"vanish like Lord Lucan at a dinner party", scopes)
	}
}

// TestConnectForwardsAuthToken ensures the auth token from NewClient is
// actually sent in the connect handshake. Because sending a message without
// credentials is like turning up to a gunfight with a banana — technically
// possible but inadvisable.
func TestConnectForwardsAuthToken(t *testing.T) {
	var upgrader = websocket.Upgrader{}
	connectFrame := make(chan []byte, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		challenge := ResponseFrame{Type: "event", Method: "challenge"}
		data, _ := json.Marshal(challenge)
		_ = conn.WriteMessage(websocket.TextMessage, data)

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		connectFrame <- msg

		resp := ResponseFrame{
			Type:   "res",
			ID:     "kapso-1",
			Result: json.RawMessage(`{"ok": true}`),
		}
		data, _ = json.Marshal(resp)
		_ = conn.WriteMessage(websocket.TextMessage, data)

		<-make(chan struct{})
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	wantToken := "surely-you-cant-be-serious-i-am-serious-and-dont-call-me-shirley"
	client := NewClient(wsURL, wantToken)

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	defer client.Close()

	raw := <-connectFrame
	var frame struct {
		Params struct {
			Auth struct {
				Token string `json:"token"`
			} `json:"auth"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if frame.Params.Auth.Token != wantToken {
		t.Fatalf("expected token %q, got %q", wantToken, frame.Params.Auth.Token)
	}
}
