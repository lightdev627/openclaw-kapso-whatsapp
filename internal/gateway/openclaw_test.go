package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// mockSigner is a test double for the Signer interface.
type mockSigner struct {
	id    string
	pubK  string
	sig   string
	ts    int64
	nonce string
}

func (m *mockSigner) DeviceID() string        { return m.id }
func (m *mockSigner) PublicKeyBase64() string { return m.pubK }
func (m *mockSigner) Sign(nonce string) (string, int64, error) {
	m.nonce = nonce
	return m.sig, m.ts, nil
}

// newTestOpenClaw creates an OpenClaw gateway pointed at the given WebSocket URL.
func newTestOpenClaw(url, token string, signer Signer) *OpenClaw {
	oc := &OpenClaw{
		url:     url,
		token:   token,
		signer:  signer,
		tracker: newReplyTracker(),
	}
	return oc
}

// TestConnectRequestsReadAndWriteScopes verifies that the gateway connect
// handshake includes both "operator.read" and "operator.write" scopes.
func TestConnectRequestsReadAndWriteScopes(t *testing.T) {
	var upgrader = websocket.Upgrader{}

	connectFrame := make(chan []byte, 1)
	serverErr := make(chan error, 1)
	done := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- fmt.Errorf("upgrade: %w", err)
			return
		}
		defer func() { _ = conn.Close() }()

		challenge := responseFrame{
			Type:   "event",
			Method: "challenge",
		}
		challengeData, _ := json.Marshal(challenge)
		if err := conn.WriteMessage(websocket.TextMessage, challengeData); err != nil {
			serverErr <- fmt.Errorf("write challenge: %w", err)
			return
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			serverErr <- fmt.Errorf("read connect: %w", err)
			return
		}
		connectFrame <- msg

		resp := responseFrame{
			Type:   "res",
			ID:     "kapso-1",
			Result: json.RawMessage(`{"ok": true}`),
		}
		respData, _ := json.Marshal(resp)
		if err := conn.WriteMessage(websocket.TextMessage, respData); err != nil {
			serverErr <- fmt.Errorf("write response: %w", err)
			return
		}

		<-done
	}))
	defer srv.Close()
	defer close(done)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	client := newTestOpenClaw(wsURL, "test-token-nobody-expects-the-spanish-inquisition", nil)

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	defer func() { _ = client.Close() }()

	select {
	case err := <-serverErr:
		t.Fatalf("server handler error: %v", err)
	default:
	}

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
		t.Errorf("missing required scope 'operator.write'; got scopes: %v", scopes)
	}
}

// TestConnectForwardsAuthToken ensures the auth token from the constructor is
// actually sent in the connect handshake.
func TestConnectForwardsAuthToken(t *testing.T) {
	var upgrader = websocket.Upgrader{}
	connectFrame := make(chan []byte, 1)
	serverErr := make(chan error, 1)
	done := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- fmt.Errorf("upgrade: %w", err)
			return
		}
		defer func() { _ = conn.Close() }()

		challenge := responseFrame{Type: "event", Method: "challenge"}
		data, _ := json.Marshal(challenge)
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			serverErr <- fmt.Errorf("write challenge: %w", err)
			return
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			serverErr <- fmt.Errorf("read connect: %w", err)
			return
		}
		connectFrame <- msg

		resp := responseFrame{
			Type:   "res",
			ID:     "kapso-1",
			Result: json.RawMessage(`{"ok": true}`),
		}
		data, _ = json.Marshal(resp)
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			serverErr <- fmt.Errorf("write response: %w", err)
			return
		}

		<-done
	}))
	defer srv.Close()
	defer close(done)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	wantToken := "surely-you-cant-be-serious-i-am-serious-and-dont-call-me-shirley"
	client := newTestOpenClaw(wsURL, wantToken, nil)

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	defer func() { _ = client.Close() }()

	select {
	case err := <-serverErr:
		t.Fatalf("server handler error: %v", err)
	default:
	}

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

// TestConnectIncludesDeviceIdentity verifies that when a Signer is provided,
// the connect request contains a populated device object.
func TestConnectIncludesDeviceIdentity(t *testing.T) {
	var upgrader = websocket.Upgrader{}
	connectFrame := make(chan []byte, 1)
	serverErr := make(chan error, 1)
	done := make(chan struct{})

	challengeNonce := "test-challenge-nonce-from-gateway"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- fmt.Errorf("upgrade: %w", err)
			return
		}
		defer func() { _ = conn.Close() }()

		challenge := fmt.Sprintf(`{"type":"event","method":"challenge","payload":{"nonce":"%s"}}`, challengeNonce)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(challenge)); err != nil {
			serverErr <- fmt.Errorf("write challenge: %w", err)
			return
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			serverErr <- fmt.Errorf("read connect: %w", err)
			return
		}
		connectFrame <- msg

		resp := responseFrame{
			Type:   "res",
			ID:     "kapso-1",
			Result: json.RawMessage(`{"ok": true}`),
		}
		data, _ := json.Marshal(resp)
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			serverErr <- fmt.Errorf("write response: %w", err)
			return
		}

		<-done
	}))
	defer srv.Close()
	defer close(done)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	signer := &mockSigner{
		id:   "device-fingerprint-abc",
		pubK: "dGVzdC1wdWJsaWMta2V5",
		sig:  "dGVzdC1zaWduYXR1cmU=",
		ts:   1737264000000,
	}

	client := newTestOpenClaw(wsURL, "test-token", signer)

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	defer func() { _ = client.Close() }()

	select {
	case err := <-serverErr:
		t.Fatalf("server handler error: %v", err)
	default:
	}

	raw := <-connectFrame

	var frame struct {
		Params struct {
			Device *struct {
				ID        string `json:"id"`
				PublicKey string `json:"publicKey"`
				Signature string `json:"signature"`
				SignedAt  int64  `json:"signedAt"`
				Nonce     string `json:"nonce"`
			} `json:"device"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("unmarshal connect frame: %v", err)
	}

	if frame.Params.Device == nil {
		t.Fatal("device field missing from connect request")
	}

	d := frame.Params.Device
	if d.ID != "device-fingerprint-abc" {
		t.Errorf("device.id: got %q, want %q", d.ID, "device-fingerprint-abc")
	}
	if d.PublicKey != "dGVzdC1wdWJsaWMta2V5" {
		t.Errorf("device.publicKey: got %q, want %q", d.PublicKey, "dGVzdC1wdWJsaWMta2V5")
	}
	if d.Signature != "dGVzdC1zaWduYXR1cmU=" {
		t.Errorf("device.signature: got %q, want %q", d.Signature, "dGVzdC1zaWduYXR1cmU=")
	}
	if d.SignedAt != 1737264000000 {
		t.Errorf("device.signedAt: got %d, want %d", d.SignedAt, 1737264000000)
	}
	if d.Nonce != challengeNonce {
		t.Errorf("device.nonce: got %q, want %q", d.Nonce, challengeNonce)
	}

	if signer.nonce != challengeNonce {
		t.Errorf("signer received nonce %q, want %q", signer.nonce, challengeNonce)
	}
}

// TestConnectWithoutSignerOmitsDevice verifies that when no Signer is
// provided, the device field is absent from the connect request.
func TestConnectWithoutSignerOmitsDevice(t *testing.T) {
	var upgrader = websocket.Upgrader{}
	connectFrame := make(chan []byte, 1)
	serverErr := make(chan error, 1)
	done := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- fmt.Errorf("upgrade: %w", err)
			return
		}
		defer func() { _ = conn.Close() }()

		challenge := responseFrame{Type: "event", Method: "challenge"}
		data, _ := json.Marshal(challenge)
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			serverErr <- fmt.Errorf("write challenge: %w", err)
			return
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			serverErr <- fmt.Errorf("read connect: %w", err)
			return
		}
		connectFrame <- msg

		resp := responseFrame{
			Type:   "res",
			ID:     "kapso-1",
			Result: json.RawMessage(`{"ok": true}`),
		}
		data, _ = json.Marshal(resp)
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			serverErr <- fmt.Errorf("write response: %w", err)
			return
		}

		<-done
	}))
	defer srv.Close()
	defer close(done)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	client := newTestOpenClaw(wsURL, "test-token", nil)

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	defer func() { _ = client.Close() }()

	select {
	case err := <-serverErr:
		t.Fatalf("server handler error: %v", err)
	default:
	}

	raw := <-connectFrame

	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var params map[string]json.RawMessage
	if err := json.Unmarshal(generic["params"], &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}

	if _, exists := params["device"]; exists {
		t.Error("device field should be absent when no signer is provided")
	}
}

// TestConnectWithSignerButNoNonceErrors verifies that when a Signer is
// configured but the gateway challenge contains no nonce, Connect returns
// a clear error.
func TestConnectWithSignerButNoNonceErrors(t *testing.T) {
	var upgrader = websocket.Upgrader{}
	done := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		challenge := responseFrame{Type: "event", Method: "challenge"}
		data, _ := json.Marshal(challenge)
		_ = conn.WriteMessage(websocket.TextMessage, data)

		<-done
	}))
	defer srv.Close()
	defer close(done)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	signer := &mockSigner{id: "test", pubK: "dGVzdA==", sig: "c2ln", ts: 1}
	client := newTestOpenClaw(wsURL, "test-token", signer)

	err := client.Connect(context.Background())
	if err == nil {
		_ = client.Close()
		t.Fatal("expected error when signer is configured but challenge has no nonce")
	}

	if !strings.Contains(err.Error(), "missing nonce") {
		t.Errorf("error should mention missing nonce, got: %v", err)
	}
}

// TestConcurrentClaimsUniqueReplies verifies that when multiple goroutines
// race to read the same session JSONL file, each one claims a different
// assistant reply.
func TestConcurrentClaimsUniqueReplies(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	lines := ""
	for i := 0; i < 3; i++ {
		ts := base.Add(time.Duration(i+1) * time.Second)
		lines += fmt.Sprintf(
			`{"type":"message","timestamp":"%s","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"reply-%d"}]}}`,
			ts.Format(time.RFC3339), i+1,
		) + "\n"
	}
	if err := os.WriteFile(sessionFile, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}

	since := base
	tracker := newReplyTracker()

	const goroutines = 3
	claimed := make([]string, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			replies, err := getAssistantReplies(sessionFile, since)
			if err != nil {
				t.Errorf("goroutine %d: getAssistantReplies: %v", g, err)
				return
			}
			for _, r := range replies {
				if tracker.claim(r.Key) {
					claimed[g] = r.Text
					return
				}
			}
		}()
	}

	wg.Wait()

	seen := map[string]int{}
	for g, text := range claimed {
		if text == "" {
			t.Errorf("goroutine %d got no reply", g)
			continue
		}
		seen[text]++
	}

	for text, count := range seen {
		if count > 1 {
			t.Errorf("reply %q was claimed %d times (want 1)", text, count)
		}
	}

	if len(seen) != goroutines {
		t.Errorf("expected %d unique replies, got %d: %v", goroutines, len(seen), seen)
	}
}
