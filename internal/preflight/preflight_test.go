package preflight

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Enriquefft/openclaw-kapso-whatsapp/internal/config"
	"github.com/Enriquefft/openclaw-kapso-whatsapp/internal/kapso"
	"github.com/gorilla/websocket"
)

func newTestConfig(apiKey, phoneID, gatewayURL string) *config.Config {
	cfg := &config.Config{}
	cfg.Kapso.APIKey = apiKey
	cfg.Kapso.PhoneNumberID = phoneID
	cfg.Gateway.URL = gatewayURL
	cfg.Delivery.Mode = "polling"
	return cfg
}

func mockKapsoServer(statusCode int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		fmt.Fprint(w, `{"data":[]}`)
	}))
}

func mockGatewayServer() *httptest.Server {
	upgrader := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.Close()
	}))
}

func TestRun_AllPass(t *testing.T) {
	ks := mockKapsoServer(http.StatusOK)
	defer ks.Close()

	gs := mockGatewayServer()
	defer gs.Close()

	gwURL := "ws" + strings.TrimPrefix(gs.URL, "http")

	client := kapso.NewClient("test-key", "test-phone")
	client.BaseURL = ks.URL

	cfg := newTestConfig("test-key", "test-phone", gwURL)

	var buf bytes.Buffer
	err := Run(cfg, &buf, &Options{
		KapsoClient: client,
	})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	output := buf.String()
	if strings.Count(output, "[OK  ]") < 4 {
		t.Errorf("expected at least 4 OK results, got:\n%s", output)
	}
	if strings.Contains(output, "[FAIL]") {
		t.Errorf("unexpected FAIL in output:\n%s", output)
	}
}

func TestRun_MissingEnvVars(t *testing.T) {
	cfg := newTestConfig("", "", "ws://127.0.0.1:1")

	var buf bytes.Buffer
	err := Run(cfg, &buf, &Options{
		GatewayDialer: func(string) error { return nil },
	})

	if err == nil {
		t.Fatal("expected error for missing env vars")
	}

	output := buf.String()
	if !strings.Contains(output, "KAPSO_API_KEY") || !strings.Contains(output, "[FAIL]") {
		t.Errorf("expected FAIL for KAPSO_API_KEY, got:\n%s", output)
	}
	if !strings.Contains(output, "KAPSO_PHONE_NUMBER_ID") {
		t.Errorf("expected FAIL for KAPSO_PHONE_NUMBER_ID, got:\n%s", output)
	}
}

func TestRun_BadKapsoCredentials(t *testing.T) {
	ks := mockKapsoServer(http.StatusUnauthorized)
	defer ks.Close()

	client := kapso.NewClient("bad-key", "test-phone")
	client.BaseURL = ks.URL

	cfg := newTestConfig("bad-key", "test-phone", "ws://127.0.0.1:1")

	var buf bytes.Buffer
	err := Run(cfg, &buf, &Options{
		KapsoClient:   client,
		GatewayDialer: func(string) error { return nil },
	})

	if err == nil {
		t.Fatal("expected error for bad credentials")
	}

	output := buf.String()
	if !strings.Contains(output, "Kapso credentials") || !strings.Contains(output, "[FAIL]") {
		t.Errorf("expected FAIL for Kapso credentials, got:\n%s", output)
	}
}

func TestRun_GatewayUnreachable(t *testing.T) {
	ks := mockKapsoServer(http.StatusOK)
	defer ks.Close()

	client := kapso.NewClient("test-key", "test-phone")
	client.BaseURL = ks.URL

	cfg := newTestConfig("test-key", "test-phone", "ws://127.0.0.1:1")

	var buf bytes.Buffer
	err := Run(cfg, &buf, &Options{
		KapsoClient: client,
		GatewayDialer: func(string) error {
			return fmt.Errorf("connection refused")
		},
	})

	if err == nil {
		t.Fatal("expected error for unreachable gateway")
	}

	output := buf.String()
	if !strings.Contains(output, "Gateway connectivity") || !strings.Contains(output, "[FAIL]") {
		t.Errorf("expected FAIL for gateway, got:\n%s", output)
	}
}

func TestRun_InvalidGatewayURL(t *testing.T) {
	ks := mockKapsoServer(http.StatusOK)
	defer ks.Close()

	client := kapso.NewClient("test-key", "test-phone")
	client.BaseURL = ks.URL

	cfg := newTestConfig("test-key", "test-phone", "http://not-websocket")

	var buf bytes.Buffer
	err := Run(cfg, &buf, &Options{KapsoClient: client})

	if err == nil {
		t.Fatal("expected error for invalid gateway URL scheme")
	}

	output := buf.String()
	if !strings.Contains(output, "invalid gateway URL") {
		t.Errorf("expected invalid URL message, got:\n%s", output)
	}
}
