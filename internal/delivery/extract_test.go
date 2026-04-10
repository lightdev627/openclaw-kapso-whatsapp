package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Enriquefft/openclaw-kapso-whatsapp/internal/kapso"
	"github.com/Enriquefft/openclaw-kapso-whatsapp/internal/transcribe"
)

// mockTranscriber is a test double for transcribe.Transcriber.
type mockTranscriber struct {
	text string
	err  error
}

func (m *mockTranscriber) Transcribe(_ context.Context, _ []byte, _ string) (string, error) {
	return m.text, m.err
}

func TestExtractText_Text(t *testing.T) {
	msg := kapso.Message{
		ID:   "m1",
		Type: "text",
		From: "+1234567890",
		Text: &kapso.TextContent{Body: "hello world"},
	}
	text, ok := ExtractText(msg, nil, nil, 0)
	if !ok {
		t.Fatal("expected ok=true for text message")
	}
	if text != "hello world" {
		t.Fatalf("got %q, want %q", text, "hello world")
	}
}

func TestExtractText_TextNilBody(t *testing.T) {
	msg := kapso.Message{
		ID:   "m2",
		Type: "text",
		From: "+1234567890",
	}
	_, ok := ExtractText(msg, nil, nil, 0)
	if ok {
		t.Fatal("expected ok=false for text message with nil Text")
	}
}

func TestExtractText_Image(t *testing.T) {
	msg := kapso.Message{
		ID:   "m3",
		Type: "image",
		From: "+1234567890",
		Image: &kapso.ImageContent{
			ID:       "media-123",
			MimeType: "image/jpeg",
			Caption:  "sunset photo",
		},
	}
	text, ok := ExtractText(msg, nil, nil, 0)
	if !ok {
		t.Fatal("expected ok=true for image message")
	}
	if !strings.Contains(text, "[image]") {
		t.Errorf("expected [image] tag in %q", text)
	}
	if !strings.Contains(text, "sunset photo") {
		t.Errorf("expected caption in %q", text)
	}
	if !strings.Contains(text, "image/jpeg") {
		t.Errorf("expected mime type in %q", text)
	}
}

func TestExtractText_ImageWithKapsoMediaURL(t *testing.T) {
	msg := kapso.Message{
		ID:   "m3b",
		Type: "image",
		From: "+1234567890",
		Image: &kapso.ImageContent{
			ID:       "media-123",
			MimeType: "image/jpeg",
			Caption:  "sunset",
		},
		Kapso: &kapso.KapsoMeta{
			HasMedia: true,
			MediaURL: "https://api.kapso.ai/media/photo.jpg",
		},
	}
	text, ok := ExtractText(msg, nil, nil, 0)
	if !ok {
		t.Fatal("expected ok=true for image message")
	}
	if !strings.Contains(text, "https://api.kapso.ai/media/photo.jpg") {
		t.Errorf("expected media URL in %q", text)
	}
}

func TestExtractText_Document(t *testing.T) {
	msg := kapso.Message{
		ID:   "m4",
		Type: "document",
		From: "+1234567890",
		Document: &kapso.DocumentContent{
			ID:       "media-456",
			MimeType: "application/pdf",
			Filename: "report.pdf",
		},
	}
	text, ok := ExtractText(msg, nil, nil, 0)
	if !ok {
		t.Fatal("expected ok=true for document message")
	}
	if !strings.Contains(text, "[document]") {
		t.Errorf("expected [document] tag in %q", text)
	}
	if !strings.Contains(text, "report.pdf") {
		t.Errorf("expected filename in %q", text)
	}
}

func TestExtractText_DocumentCaptionFallback(t *testing.T) {
	msg := kapso.Message{
		ID:   "m4b",
		Type: "document",
		From: "+1234567890",
		Document: &kapso.DocumentContent{
			ID:       "media-456",
			MimeType: "application/pdf",
			Caption:  "my report",
		},
	}
	text, ok := ExtractText(msg, nil, nil, 0)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !strings.Contains(text, "my report") {
		t.Errorf("expected caption fallback in %q", text)
	}
}

func TestExtractText_Audio(t *testing.T) {
	msg := kapso.Message{
		ID:   "m5",
		Type: "audio",
		From: "+1234567890",
		Audio: &kapso.AudioContent{
			ID:       "media-789",
			MimeType: "audio/ogg",
		},
	}
	text, ok := ExtractText(msg, nil, nil, 0)
	if !ok {
		t.Fatal("expected ok=true for audio message")
	}
	if !strings.Contains(text, "[audio]") {
		t.Errorf("expected [audio] tag in %q", text)
	}
	if !strings.Contains(text, "audio/ogg") {
		t.Errorf("expected mime type in %q", text)
	}
}

func TestExtractText_Video(t *testing.T) {
	msg := kapso.Message{
		ID:   "m6",
		Type: "video",
		From: "+1234567890",
		Video: &kapso.VideoContent{
			ID:       "media-v1",
			MimeType: "video/mp4",
			Caption:  "funny clip",
		},
	}
	text, ok := ExtractText(msg, nil, nil, 0)
	if !ok {
		t.Fatal("expected ok=true for video message")
	}
	if !strings.Contains(text, "[video]") {
		t.Errorf("expected [video] tag in %q", text)
	}
	if !strings.Contains(text, "funny clip") {
		t.Errorf("expected caption in %q", text)
	}
}

func TestExtractText_Location(t *testing.T) {
	msg := kapso.Message{
		ID:   "m7",
		Type: "location",
		From: "+1234567890",
		Location: &kapso.LocationContent{
			Latitude:  -12.046374,
			Longitude: -77.042793,
			Name:      "Lima",
			Address:   "Peru",
		},
	}
	text, ok := ExtractText(msg, nil, nil, 0)
	if !ok {
		t.Fatal("expected ok=true for location message")
	}
	if !strings.Contains(text, "[location]") {
		t.Errorf("expected [location] tag in %q", text)
	}
	if !strings.Contains(text, "Lima") {
		t.Errorf("expected name in %q", text)
	}
	if !strings.Contains(text, "-12.046374") {
		t.Errorf("expected latitude in %q", text)
	}
}

func TestExtractText_UnsupportedType(t *testing.T) {
	type capture struct {
		to, body string
	}
	ch := make(chan capture, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req kapso.SendMessageRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		ch <- capture{to: req.To, body: req.Text.Body}
		_ = json.NewEncoder(w).Encode(kapso.SendMessageResponse{})
	}))
	defer srv.Close()

	client := &kapso.Client{
		APIKey:        "test-key",
		PhoneNumberID: "12345",
		HTTPClient:    &http.Client{Transport: &rewriteTransport{base: srv.URL, wrapped: http.DefaultTransport}},
	}

	msg := kapso.Message{
		ID:   "m8",
		Type: "sticker",
		From: "+1234567890",
		Sticker: &kapso.StickerContent{
			ID:       "stk-1",
			MimeType: "image/webp",
		},
	}
	_, ok := ExtractText(msg, client, nil, 0)
	if ok {
		t.Fatal("expected ok=false for unsupported sticker type")
	}

	got := <-ch
	if got.to != "+1234567890" {
		t.Errorf("notification sent to %q, want %q", got.to, "+1234567890")
	}
	if !strings.Contains(got.body, "sticker") {
		t.Errorf("notification body %q should mention sticker", got.body)
	}
}

func TestExtractText_NilMediaContent(t *testing.T) {
	for _, typ := range []string{"image", "document", "audio", "video", "location"} {
		msg := kapso.Message{
			ID:   "nil-" + typ,
			Type: typ,
			From: "+1234567890",
		}
		_, ok := ExtractText(msg, nil, nil, 0)
		if ok {
			t.Errorf("expected ok=false for %s with nil content", typ)
		}
	}
}

func TestFormatMediaMessage_AllParts(t *testing.T) {
	text := formatMediaMessage("image", "my photo", "image/png", nil)
	want := "[image] my photo (image/png)"
	if text != want {
		t.Fatalf("got %q, want %q", text, want)
	}
}

func TestFormatMediaMessage_NoLabel(t *testing.T) {
	text := formatMediaMessage("audio", "", "audio/ogg", nil)
	want := "[audio] (audio/ogg)"
	if text != want {
		t.Fatalf("got %q, want %q", text, want)
	}
}

func TestFormatMediaMessage_WithKapsoURL(t *testing.T) {
	k := &kapso.KapsoMeta{MediaURL: "https://api.kapso.ai/media/file.jpg"}
	text := formatMediaMessage("image", "photo", "image/jpeg", k)
	if !strings.Contains(text, "https://api.kapso.ai/media/file.jpg") {
		t.Errorf("expected media URL in %q", text)
	}
}

func TestFormatLocationMessage(t *testing.T) {
	loc := &kapso.LocationContent{
		Latitude:  40.714268,
		Longitude: -74.005974,
		Name:      "New York",
		Address:   "NY, USA",
	}
	text := formatLocationMessage(loc)
	if !strings.HasPrefix(text, "[location]") {
		t.Errorf("expected [location] prefix in %q", text)
	}
	if !strings.Contains(text, "New York") {
		t.Errorf("expected name in %q", text)
	}
	if !strings.Contains(text, "NY, USA") {
		t.Errorf("expected address in %q", text)
	}
	if !strings.Contains(text, "40.714268") {
		t.Errorf("expected latitude in %q", text)
	}
}

func TestFormatLocationMessage_Minimal(t *testing.T) {
	loc := &kapso.LocationContent{
		Latitude:  0,
		Longitude: 0,
	}
	text := formatLocationMessage(loc)
	want := fmt.Sprintf("[location] (%.6f, %.6f)", 0.0, 0.0)
	if text != want {
		t.Fatalf("got %q, want %q", text, want)
	}
}

// TestExtractText_AudioServerTranscript tests that server-side transcription
// from Kapso is preferred over local transcription.
func TestExtractText_AudioServerTranscript(t *testing.T) {
	msg := kapso.Message{
		ID:   "audio-server-001",
		Type: "audio",
		From: "+1234567890",
		Audio: &kapso.AudioContent{
			ID:       "media-audio-001",
			MimeType: "audio/ogg",
		},
		Kapso: &kapso.KapsoMeta{
			HasMedia: true,
			MediaURL: "https://api.kapso.ai/media/voice.ogg",
			Transcript: &kapso.Transcript{
				Text: "Hello, I need help with my order",
			},
		},
	}

	// Even with a local transcriber configured, server-side transcript wins.
	text, ok := ExtractText(msg, nil, &mockTranscriber{text: "local result"}, 1024*1024)
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := "[voice] Hello, I need help with my order"
	if text != want {
		t.Fatalf("got %q, want %q", text, want)
	}
}

// TestExtractText_AudioLocalTranscription tests local transcription via
// DownloadMedia when no server-side transcript is available.
func TestExtractText_AudioLocalTranscription(t *testing.T) {
	const rawAudio = "fake-audio-bytes"

	// Test server serves audio download.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rawAudio))
	}))
	defer srv.Close()

	client := &kapso.Client{
		APIKey:        "test-key",
		PhoneNumberID: "12345",
		HTTPClient: &http.Client{
			Transport: &rewriteTransport{base: srv.URL, wrapped: http.DefaultTransport},
		},
	}

	tests := []struct {
		name        string
		transcriber transcribe.Transcriber
		kapso       *kapso.KapsoMeta
		wantPrefix  string
	}{
		{
			name:        "success",
			transcriber: &mockTranscriber{text: "hello world", err: nil},
			kapso: &kapso.KapsoMeta{
				HasMedia: true,
				MediaURL: "https://api.kapso.ai/media/voice.ogg",
			},
			wantPrefix: "[voice] hello world",
		},
		{
			name:        "transcription_error_falls_back",
			transcriber: &mockTranscriber{text: "", err: fmt.Errorf("mock error")},
			kapso: &kapso.KapsoMeta{
				HasMedia: true,
				MediaURL: "https://api.kapso.ai/media/voice.ogg",
			},
			wantPrefix: "[audio]",
		},
		{
			name:        "nil_transcriber_falls_back",
			transcriber: nil,
			kapso: &kapso.KapsoMeta{
				HasMedia: true,
				MediaURL: "https://api.kapso.ai/media/voice.ogg",
			},
			wantPrefix: "[audio]",
		},
		{
			name:        "no_media_url_falls_back",
			transcriber: &mockTranscriber{text: "hello", err: nil},
			kapso:       nil,
			wantPrefix:  "[audio]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := kapso.Message{
				ID:   "audio-msg-001",
				Type: "audio",
				From: "+1234567890",
				Audio: &kapso.AudioContent{
					ID:       "audio-media-001",
					MimeType: "audio/ogg",
				},
				Kapso: tc.kapso,
			}

			text, ok := ExtractText(msg, client, tc.transcriber, 1024*1024)
			if !ok {
				t.Fatal("expected ok=true")
			}
			if !strings.HasPrefix(text, tc.wantPrefix) {
				t.Errorf("text=%q, want prefix %q", text, tc.wantPrefix)
			}
		})
	}
}

// TestExtractText_AudioNilContent verifies that a nil Audio field returns ("", false).
func TestExtractText_AudioNilContent(t *testing.T) {
	msg := kapso.Message{
		ID:   "audio-nil",
		Type: "audio",
		From: "+1234567890",
		// Audio is nil
	}
	text, ok := ExtractText(msg, nil, &mockTranscriber{text: "should not be called"}, 1024)
	if ok {
		t.Fatal("expected ok=false for audio message with nil Audio content")
	}
	if text != "" {
		t.Fatalf("expected empty text, got %q", text)
	}
}

// rewriteTransport rewrites all request URLs to point at the test server.
type rewriteTransport struct {
	base    string
	wrapped http.RoundTripper
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.base, "http://")
	return t.wrapped.RoundTrip(req)
}
