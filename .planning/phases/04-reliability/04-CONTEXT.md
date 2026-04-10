# Phase 4: Reliability - Context

**Gathered:** 2026-03-01
**Status:** Ready for planning

<domain>
## Phase Boundary

Harden transcription against transient failures, duplicate billing, and silent hallucinations. Add content-hash cache, no_speech_prob guard, and debug logging. Retry wrapper already exists from Phase 2 — verify it meets criteria, don't rebuild.

</domain>

<decisions>
## Implementation Decisions

### Cache behavior
- In-memory only (map+mutex or sync.Map) — lost on restart, fits the daemon model since WhatsApp rarely resends the same audio
- Default TTL: 1 hour — covers common WhatsApp dedup retries (usually within minutes), low memory footprint
- No max entry limit — rely on TTL expiry only. Audio volume is low for most users
- Cache key: SHA-256 of audio bytes — crypto-strength collision resistance, standard and safe
- Cache wraps the Transcriber interface (decorator pattern) — sits between retry wrapper and caller

### Hallucination guard
- no_speech_prob threshold configurable in config.toml with default 0.85 — roadmap says "configured threshold" implying configurability
- Guard applies to OpenAI/Groq only (they support verbose_json natively) — Deepgram has its own confidence metrics, local whisper doesn't return verbose_json
- When threshold exceeded, log the actual probability value: `WARN: no_speech_prob=0.92 exceeds threshold 0.85, falling back to [audio]` — helps operators debug false rejections
- Fallback produces `[audio] (mime)` same as any other transcription failure

### Debug logging
- Controlled via config flag + env override: `debug` bool in TranscribeConfig, overridable with `KAPSO_TRANSCRIBE_DEBUG=true` — follows existing 3-tier config pattern
- Required fields: avg_logprob, no_speech_prob, detected language (matches roadmap criterion #5 exactly)
- Also log transcription duration (ms) and provider/model used — useful for performance monitoring
- Uses standard `log.Printf` with `[transcribe:debug]` prefix — no new logging framework

### Retry tuning
- Verify existing retry.go meets criteria, no changes needed — already has 3 attempts, 1s base, 2x factor, jitter, context cancellation
- Do NOT make retry params configurable — current defaults match success criteria and adding knobs adds complexity without clear benefit

### Claude's Discretion
- Exact cache cleanup goroutine implementation (ticker-based eviction vs lazy expiry)
- How to restructure verbose_json parsing in openai.go to extract no_speech_prob
- Whether cache decorator is a separate file or added to existing retry.go

</decisions>

<specifics>
## Specific Ideas

- Cache should be a Transcriber decorator so it composes cleanly: `New()` returns `cache(retry(provider))` for cloud, `cache(provider)` for local
- The verbose_json response from OpenAI already has `segments[].no_speech_prob` and `segments[].avg_logprob` — parse the full response struct instead of just `{text}`
- Deepgram equivalent would be its confidence field but that's a different API shape — keep it simple, guard OpenAI/Groq only

</specifics>

<code_context>
## Existing Code Insights

### Reusable Assets
- `retryTranscriber` in retry.go: decorator pattern with injectable `sleepFunc` — cache can follow the same decorator approach
- `httpError` type in openai.go: used by both providers for error classification
- `mockTranscriber` in retry_test.go: reusable for cache tests
- `config.TranscribeConfig`: already has `Timeout`, `Provider`, `Model` fields — add `Debug`, `NoSpeechThreshold`, `CacheTTL`

### Established Patterns
- Decorator/wrapper pattern: `retryTranscriber` wraps `Transcriber` — cache should wrap similarly
- Table-driven tests with dependency injection (sleepFunc, now(), execCmd)
- 3-tier config: defaults < TOML file < env vars
- `log.Printf` for all logging, no frameworks

### Integration Points
- `transcribe.New()` factory: compose decorators here — `cache(retry(provider))` or `cache(provider)`
- `openai.go:Transcribe()`: currently discards verbose_json fields, needs to parse full response for no_speech_prob/avg_logprob
- `config.go:applyEnv()`: add env var overrides for new config fields
- `config.go:defaults()`: add defaults for new fields

</code_context>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 04-reliability*
*Context gathered: 2026-03-01*
