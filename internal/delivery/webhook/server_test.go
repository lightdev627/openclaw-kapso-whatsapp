package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Enriquefft/openclaw-kapso-whatsapp/internal/delivery"
	"github.com/Enriquefft/openclaw-kapso-whatsapp/internal/kapso"
)

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		name string
		json string
		want webhookFormat
	}{
		{
			name: "kapso_native",
			json: `{"type":"whatsapp.message.received","data":[]}`,
			want: formatKapso,
		},
		{
			name: "meta_format",
			json: `{"object":"whatsapp_business_account","entry":[]}`,
			want: formatMeta,
		},
		{
			name: "invalid_json",
			json: `not json`,
			want: formatUnknown,
		},
		{
			name: "empty_object",
			json: `{}`,
			want: formatUnknown,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := detectFormat([]byte(tc.json))
			if got != tc.want {
				t.Errorf("detectFormat(%q) = %v, want %v", tc.json, got, tc.want)
			}
		})
	}
}

func computeHMAC(body, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestValidateSignature(t *testing.T) {
	body := `{"test":"data"}`
	secret := "test-secret"
	validSig := computeHMAC(body, secret)

	tests := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{
			name:    "valid_kapso_signature",
			headers: map[string]string{"X-Webhook-Signature": validSig},
			want:    true,
		},
		{
			name:    "valid_meta_signature",
			headers: map[string]string{"X-Hub-Signature-256": "sha256=" + validSig},
			want:    true,
		},
		{
			name:    "wrong_kapso_signature",
			headers: map[string]string{"X-Webhook-Signature": "deadbeef"},
			want:    false,
		},
		{
			name:    "wrong_meta_signature",
			headers: map[string]string{"X-Hub-Signature-256": "sha256=deadbeef"},
			want:    false,
		},
		{
			name:    "no_signature_headers",
			headers: map[string]string{},
			want:    false,
		},
		{
			name:    "kapso_preferred_over_meta",
			headers: map[string]string{"X-Webhook-Signature": validSig, "X-Hub-Signature-256": "sha256=wrong"},
			want:    true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range tc.headers {
				h.Set(k, v)
			}
			got := validateSignature(h, []byte(body), secret)
			if got != tc.want {
				t.Errorf("validateSignature() = %v, want %v", got, tc.want)
			}
		})
	}
}

// newTestServer creates a Server with no HMAC validation and no transcriber.
func newTestServer() *Server {
	return &Server{
		Client: &kapso.Client{
			APIKey:        "test",
			PhoneNumberID: "12345",
		},
	}
}

func TestHandleEvent_KapsoFormat(t *testing.T) {
	payload := kapso.KapsoWebhookPayload{
		Type: "whatsapp.message.received",
		Data: []kapso.KapsoWebhookEvent{
			{
				Message: kapso.Message{
					ID:        "wamid.abc123",
					From:      "5511999999999",
					Timestamp: "1700000000",
					Type:      "text",
					Text:      &kapso.TextContent{Body: "Hello from Kapso"},
					Kapso: &kapso.KapsoMeta{
						Direction:   "inbound",
						Status:      "received",
						ContactName: "Test User",
					},
				},
				PhoneNumberID: "phone-123",
			},
		},
	}
	body, _ := json.Marshal(payload)

	out := make(chan delivery.Event, 1)
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	w := httptest.NewRecorder()

	srv.handleEvent(w, req, out)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	if len(out) != 1 {
		t.Fatalf("expected 1 event, got %d", len(out))
	}

	evt := <-out
	if evt.ID != "wamid.abc123" {
		t.Errorf("ID = %q, want %q", evt.ID, "wamid.abc123")
	}
	if evt.From != "5511999999999" {
		t.Errorf("From = %q, want %q", evt.From, "5511999999999")
	}
	if evt.Name != "Test User" {
		t.Errorf("Name = %q, want %q", evt.Name, "Test User")
	}
	if evt.Text != "Hello from Kapso" {
		t.Errorf("Text = %q, want %q", evt.Text, "Hello from Kapso")
	}
}

func TestHandleEvent_MetaFormat(t *testing.T) {
	payload := kapso.WebhookPayload{
		Object: "whatsapp_business_account",
		Entry: []kapso.Entry{
			{
				ID: "entry-1",
				Changes: []kapso.Change{
					{
						Field: "messages",
						Value: kapso.ChangeValue{
							Contacts: []kapso.Contact{
								{Profile: kapso.ContactProfile{Name: "Meta User"}, WaID: "5511888888888"},
							},
							Messages: []kapso.Message{
								{
									ID:        "wamid.meta456",
									From:      "5511888888888",
									Timestamp: "1700000000",
									Type:      "text",
									Text:      &kapso.TextContent{Body: "Hello from Meta"},
								},
							},
						},
					},
				},
			},
		},
	}
	body, _ := json.Marshal(payload)

	out := make(chan delivery.Event, 1)
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	w := httptest.NewRecorder()

	srv.handleEvent(w, req, out)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	if len(out) != 1 {
		t.Fatalf("expected 1 event, got %d", len(out))
	}

	evt := <-out
	if evt.ID != "wamid.meta456" {
		t.Errorf("ID = %q, want %q", evt.ID, "wamid.meta456")
	}
	if evt.Name != "Meta User" {
		t.Errorf("Name = %q, want %q", evt.Name, "Meta User")
	}
	if evt.Text != "Hello from Meta" {
		t.Errorf("Text = %q, want %q", evt.Text, "Hello from Meta")
	}
}

func TestHandleEvent_IgnoresNonMessageEvents(t *testing.T) {
	payload := kapso.KapsoWebhookPayload{
		Type: "whatsapp.message.sent",
		Data: []kapso.KapsoWebhookEvent{
			{
				Message: kapso.Message{
					ID:   "wamid.sent1",
					From: "5511999999999",
					Type: "text",
					Text: &kapso.TextContent{Body: "outbound msg"},
				},
			},
		},
	}
	body, _ := json.Marshal(payload)

	out := make(chan delivery.Event, 1)
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	w := httptest.NewRecorder()

	srv.handleEvent(w, req, out)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	if len(out) != 0 {
		t.Fatalf("expected 0 events for non-message event, got %d", len(out))
	}
}

func TestHandleEvent_SignatureRejection(t *testing.T) {
	payload := `{"type":"whatsapp.message.received","data":[]}`

	out := make(chan delivery.Event, 1)
	srv := newTestServer()
	srv.AppSecret = "my-secret"

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(payload))
	req.Header.Set("X-Webhook-Signature", "invalid-signature")
	w := httptest.NewRecorder()

	srv.handleEvent(w, req, out)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	if len(out) != 0 {
		t.Fatalf("expected 0 events after signature rejection, got %d", len(out))
	}
}

func TestHandleEvent_ValidKapsoSignature(t *testing.T) {
	payload := kapso.KapsoWebhookPayload{
		Type: "whatsapp.message.received",
		Data: []kapso.KapsoWebhookEvent{
			{
				Message: kapso.Message{
					ID:   "wamid.signed1",
					From: "5511999999999",
					Type: "text",
					Text: &kapso.TextContent{Body: "signed message"},
					Kapso: &kapso.KapsoMeta{
						ContactName: "Signed User",
					},
				},
			},
		},
	}
	body, _ := json.Marshal(payload)

	secret := "webhook-secret"
	sig := computeHMAC(string(body), secret)

	out := make(chan delivery.Event, 1)
	srv := newTestServer()
	srv.AppSecret = secret

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-Webhook-Signature", sig)
	w := httptest.NewRecorder()

	srv.handleEvent(w, req, out)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	if len(out) != 1 {
		t.Fatalf("expected 1 event, got %d", len(out))
	}

	evt := <-out
	if evt.Text != "signed message" {
		t.Errorf("Text = %q, want %q", evt.Text, "signed message")
	}
}

func TestFormatName(t *testing.T) {
	tests := []struct {
		f    webhookFormat
		want string
	}{
		{formatKapso, "kapso-native"},
		{formatMeta, "meta"},
		{formatUnknown, "unknown"},
	}
	for _, tc := range tests {
		if got := formatName(tc.f); got != tc.want {
			t.Errorf("formatName(%d) = %q, want %q", tc.f, got, tc.want)
		}
	}
}
