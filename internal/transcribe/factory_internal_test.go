package transcribe

import (
	"testing"

	"github.com/Enriquefft/openclaw-kapso-whatsapp/internal/config"
)

// TestNewWrapsCloudProvidersWithCacheAndRetry verifies that all cloud providers
// (openai, groq, deepgram) are wrapped as cache(retry(provider)) by the factory
// when CacheTTL > 0.
func TestNewWrapsCloudProvidersWithCacheAndRetry(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.TranscribeConfig
	}{
		{
			name: "openai wrapped in cache(retry)",
			cfg:  config.TranscribeConfig{Provider: "openai", APIKey: "sk-test", CacheTTL: 3600},
		},
		{
			name: "groq wrapped in cache(retry)",
			cfg:  config.TranscribeConfig{Provider: "groq", APIKey: "gsk-test", CacheTTL: 3600},
		},
		{
			name: "deepgram wrapped in cache(retry)",
			cfg:  config.TranscribeConfig{Provider: "deepgram", APIKey: "dg-test", CacheTTL: 3600},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := New(tc.cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got == nil {
				t.Fatal("expected non-nil transcriber")
			}
			// Outermost wrapper must be *cacheTranscriber.
			ct, ok := got.(*cacheTranscriber)
			if !ok {
				t.Fatalf("factory returned %T, want *cacheTranscriber", got)
			}
			// Inner wrapper must be *retryTranscriber.
			if _, ok := ct.inner.(*retryTranscriber); !ok {
				t.Errorf("cache.inner is %T, want *retryTranscriber", ct.inner)
			}
		})
	}
}

// TestNewCacheTTLZeroSkipsCache verifies that when CacheTTL == 0, the factory
// returns a *retryTranscriber directly (no cache wrapping) for cloud providers.
func TestNewCacheTTLZeroSkipsCache(t *testing.T) {
	cfg := config.TranscribeConfig{Provider: "openai", APIKey: "sk-test", CacheTTL: 0}
	got, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil transcriber")
	}
	if _, ok := got.(*retryTranscriber); !ok {
		t.Errorf("factory returned %T, want *retryTranscriber when CacheTTL=0", got)
	}
}

// TestNewLocalProviderWithCacheTTL verifies that the local provider is wrapped
// as cache(localWhisper) when CacheTTL > 0.
// This test requires whisper-cli and ffmpeg to be in PATH — it is skipped if
// either binary is not available (e.g., in CI without local ML tooling).
func TestNewLocalProviderWithCacheTTL(t *testing.T) {
	cfg := config.TranscribeConfig{
		Provider:   "local",
		BinaryPath: "echo", // use "echo" which is always available
		CacheTTL:   3600,
	}

	got, err := New(cfg)
	if err != nil {
		// ffmpeg not in PATH — skip rather than fail.
		t.Skipf("skipping local provider test (binary not available): %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil transcriber")
	}

	ct, ok := got.(*cacheTranscriber)
	if !ok {
		t.Fatalf("factory returned %T, want *cacheTranscriber for local provider", got)
	}
	if _, ok := ct.inner.(*localWhisper); !ok {
		t.Errorf("cache.inner is %T, want *localWhisper", ct.inner)
	}
}
