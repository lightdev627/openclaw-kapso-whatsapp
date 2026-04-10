# Requirements: Kapso WhatsApp Voice Transcription

**Defined:** 2026-03-01
**Core Value:** Audio messages from WhatsApp users reach OpenClaw as usable text — transparently, reliably, with graceful fallback if transcription fails.

## v1 Requirements

Requirements for voice transcription milestone. Each maps to roadmap phases.

### Transcription Core

- [ ] **TRNS-01**: Transcriber interface with single method: `Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error)`
- [ ] **TRNS-02**: Transcribed audio enters pipeline as `[voice] ` + transcript, identical to typed text
- [ ] **TRNS-03**: Transcription failure falls back to `[audio] (mime)` with log warning (zero message loss)
- [ ] **TRNS-04**: `no_speech_prob` quality guard — high probability of silence/noise falls back to `[audio]` instead of sending hallucinated text (configurable threshold, default 0.85)
- [ ] **TRNS-05**: Audio content-hash caching — SHA-256 hash of audio bytes, in-memory map with TTL, avoids duplicate API calls on webhook retries

### Providers — Cloud

- [ ] **PROV-01**: OpenAI Whisper provider — `POST /v1/audio/transcriptions`, multipart form (file, model, language), configurable model (default `whisper-1`)
- [ ] **PROV-02**: Groq Whisper provider — same multipart shape as OpenAI with different base URL (`api.groq.com/openai/v1`), configurable model (default `whisper-large-v3`)
- [ ] **PROV-03**: Deepgram Nova provider — `POST /v1/listen`, binary body with Content-Type set to audio MIME, query params (model, smart_format, language), configurable model (default `nova-3`)
- [ ] **PROV-04**: OpenAI and Groq share implementation via configurable `BaseURL` field — no duplicated code

### Providers — Local

- [ ] **LOCL-01**: Local whisper.cpp provider — write audio to temp file, exec whisper-cli with `exec.CommandContext`, capture stdout
- [ ] **LOCL-02**: OGG/Opus to WAV conversion via ffmpeg before whisper.cpp processing
- [ ] **LOCL-03**: Configurable binary path and model path for whisper.cpp
- [ ] **LOCL-04**: Temp files cleaned up after use (including on context cancellation)

### Media Download

- [ ] **MEDL-01**: `DownloadMedia(url string) ([]byte, error)` method on Kapso client
- [ ] **MEDL-02**: Authenticates with existing API key header
- [ ] **MEDL-03**: Enforces configurable max size limit (default 25MB) via `io.LimitReader`
- [ ] **MEDL-04**: Downloads immediately at call site — media URLs expire in ~5 minutes

### Configuration

- [ ] **CONF-01**: `[transcribe]` TOML section with provider, api_key, model, language, max_audio_size
- [ ] **CONF-02**: Env overrides: `TRANSCRIBE_PROVIDER`, `TRANSCRIBE_API_KEY`, `TRANSCRIBE_MODEL`, `TRANSCRIBE_LANGUAGE`
- [ ] **CONF-03**: 3-tier precedence preserved: defaults < file < env
- [ ] **CONF-04**: Empty/missing provider = transcription disabled (backward compatible, zero behavior change)
- [ ] **CONF-05**: Default language support for Spanish and English (language hint configurable, auto-detect when empty)

### Infrastructure

- [ ] **INFR-01**: Retry with exponential backoff on 429/5xx — max 3 attempts, base 1s, factor 2x, jitter
- [ ] **INFR-02**: `context.WithTimeout` per transcription call to prevent pipeline blocking
- [ ] **INFR-03**: OGG/Opus MIME normalization — use `mime/multipart.CreatePart` (not `CreateFormFile`) for correct Content-Type
- [ ] **INFR-04**: Debug-level logging of `avg_logprob`, `no_speech_prob`, and detected language from verbose_json responses

### Wiring

- [ ] **WIRE-01**: Build Transcriber from config at startup in main.go (nil if disabled)
- [ ] **WIRE-02**: Pass Transcriber to delivery layer — no new goroutines, transcription synchronous within message processing
- [ ] **WIRE-03**: ExtractText receives optional Transcriber (nil = disabled, current behavior preserved)

### Tests

- [ ] **TEST-01**: Table-driven tests for each cloud provider with HTTP test server mocking API responses
- [ ] **TEST-02**: Local whisper.cpp provider test with mock exec
- [ ] **TEST-03**: Extract integration test with mock transcriber (success + failure fallback)
- [ ] **TEST-04**: Media download test with size limit enforcement
- [ ] **TEST-05**: Retry logic test (429, 5xx, success after retry, exhausted retries)
- [ ] **TEST-06**: Content-hash cache test (hit, miss, TTL expiry)

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Enhancements

- **ENH-01**: Whisper prompt/context injection — bias transcription toward domain vocabulary
- **ENH-02**: Video note audio track extraction and transcription
- **ENH-03**: Per-provider rate limit awareness with adaptive throttling

## Out of Scope

| Feature | Reason |
|---------|--------|
| Speaker diarization | WhatsApp voice notes are single-sender, short clips — diarization adds cost/complexity for a non-existent problem |
| Profanity filtering at STT layer | Corrupts intent before AI agent sees it — content policy belongs at agent level |
| Real-time streaming STT | Voice notes are pre-recorded files, not live streams — streaming adds WebSocket complexity for zero benefit |
| TTS audio replies | Separate milestone — different API, delivery format, and WhatsApp media sending requirements |
| On-device model auto-download | Models are 75MB–2.9GB — operator should control model selection and staging |
| Google Cloud STT | Adds heavy SDK dependency, inconsistent with minimal deps convention |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| _(populated by roadmapper)_ | | |

**Coverage:**
- v1 requirements: 30 total
- Mapped to phases: 0
- Unmapped: 30 _(pending roadmap)_

---
*Requirements defined: 2026-03-01*
*Last updated: 2026-03-01 after initial definition*
