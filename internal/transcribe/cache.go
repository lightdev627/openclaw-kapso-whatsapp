package transcribe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// cacheEntry holds a cached transcript and its expiry time.
type cacheEntry struct {
	text   string
	expiry time.Time
}

// cacheTranscriber is a Transcriber decorator that caches results by SHA-256
// hash of the audio bytes for a configurable TTL. Errors are never cached.
//
// The mutex is released before calling the inner Transcriber to avoid holding
// a lock during potentially long network calls (see RESEARCH.md Pitfall 1).
type cacheTranscriber struct {
	inner   Transcriber
	ttl     time.Duration
	nowFunc func() time.Time
	mu      sync.Mutex
	items   map[string]cacheEntry
}

// newCacheTranscriber creates a cacheTranscriber wrapping inner with the given TTL.
func newCacheTranscriber(inner Transcriber, ttl time.Duration) *cacheTranscriber {
	return &cacheTranscriber{
		inner:   inner,
		ttl:     ttl,
		nowFunc: time.Now,
		items:   make(map[string]cacheEntry),
	}
}

// cacheKey returns the SHA-256 hex digest of audio as the cache key.
func (c *cacheTranscriber) cacheKey(audio []byte) string {
	h := sha256.Sum256(audio)
	return hex.EncodeToString(h[:])
}

// Transcribe returns a cached result if a non-expired entry exists for the
// audio bytes. On a cache miss it calls the inner Transcriber and stores the
// result (only on success).
func (c *cacheTranscriber) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	key := c.cacheKey(audio)
	now := c.nowFunc()

	// Check for a valid (non-expired) cache entry.
	c.mu.Lock()
	entry, ok := c.items[key]
	if ok && now.Before(entry.expiry) {
		c.mu.Unlock()
		return entry.text, nil
	}
	c.mu.Unlock()

	// Cache miss (or expired): call inner without holding the lock.
	text, err := c.inner.Transcribe(ctx, audio, mimeType)
	if err != nil {
		// Do not cache errors — allow the next call to retry.
		return "", err
	}

	// Store successful result.
	expiry := c.nowFunc().Add(c.ttl)
	c.mu.Lock()
	c.items[key] = cacheEntry{text: text, expiry: expiry}
	c.mu.Unlock()

	return text, nil
}
