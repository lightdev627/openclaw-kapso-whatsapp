# Phase 1: Foundation - Context

**Gathered:** 2026-03-01
**Status:** Ready for planning

<domain>
## Phase Boundary

Define the transcription contract and audio download capability. Delivers: `[transcribe]` TOML config section with env overrides, `Transcriber` interface with factory function, and `DownloadMedia` method on the Kapso client with size-limit enforcement. No providers are implemented — that's Phase 2+.

</domain>

<decisions>
## Implementation Decisions

### Env var naming
- Use `KAPSO_TRANSCRIBE_*` prefix for all transcription env vars (consistent with existing `KAPSO_*` convention)
- Full list: `KAPSO_TRANSCRIBE_PROVIDER`, `KAPSO_TRANSCRIBE_API_KEY`, `KAPSO_TRANSCRIBE_MODEL`, `KAPSO_TRANSCRIBE_LANGUAGE`, `KAPSO_TRANSCRIBE_MAX_AUDIO_SIZE`
- Local provider paths: `KAPSO_TRANSCRIBE_BINARY_PATH`, `KAPSO_TRANSCRIBE_MODEL_PATH`
- Separate `KAPSO_TRANSCRIBE_API_KEY` — no fallback to `KAPSO_API_KEY` (different services)
- Every config field gets an env override — no exceptions

### Provider strings
- Service names: `"openai"`, `"groq"`, `"deepgram"`, `"local"` — four valid strings, nothing else
- Case-insensitive matching (lowercase internally, matches existing `resolveMode()` pattern in config.go)
- No aliases — `"whisper"`, `"nova"`, etc. are rejected
- Model field is optional; each provider has a hardcoded default (`whisper-1`, `whisper-large-v3`, `nova-3`)

### Startup error behavior
- Unknown provider string → crash at startup with clear error (matches success criteria SC-3)
- Cloud provider set but API key missing → crash: `"provider 'groq' requires KAPSO_TRANSCRIBE_API_KEY"`
- Local provider: verify binary exists and is executable during `New(cfg)` — crash if not found
- Provider empty/missing → transcription disabled, log info: `"transcription disabled (no provider configured)"`
- Philosophy: fail fast on config errors, silent operation when intentionally disabled

### Config defaults
- `max_audio_size`: 25MB (generous headroom over WhatsApp's practical limit)
- `language`: empty string = auto-detect (Whisper/Deepgram handle multilingual including Spanish and English)
- Local `binary_path`: defaults to `"whisper-cli"` (found via PATH); `model_path`: empty (whisper-cli uses its own default)
- Include `timeout` field in config struct now (default 30s) — ready for Phase 3 wiring without config changes
- Defer `no_speech_threshold` to Phase 4 — only add when used (YAGNI)

### Claude's Discretion
- Internal package structure (e.g., `internal/transcribe/` naming)
- TranscribeConfig struct field ordering and naming conventions
- Exact error message wording
- Test structure and mock patterns for media download

</decisions>

<specifics>
## Specific Ideas

- Env override pattern should mirror existing `applyEnv()` function in config.go — same style, same flow
- Provider case normalization should use the same pattern as `resolveMode()` with `strings.ToLower()`
- `DownloadMedia` should use `io.LimitReader` per MEDL-03 — not `Content-Length` header checking

</specifics>

<code_context>
## Existing Code Insights

### Reusable Assets
- `config.Load()` + `applyEnv()`: 3-tier precedence pattern ready to extend with `[transcribe]` section
- `kapso.Client`: Has `GetMediaURL(mediaID)` returning `MediaResponse` with URL — `DownloadMedia` builds on this
- `kapso.AudioContent`: Already has ID, MimeType, SHA256 fields for audio messages
- `delivery.ExtractText`: Handles `"audio"` case, formats as `[audio] (mime)` — Phase 3 will add transcription branch here

### Established Patterns
- TOML struct tags with `toml:"field_name"` for config parsing
- `resolveMode()` for case-insensitive string normalization with switch statement
- `http.Client` injection via struct field for testability (see `kapso.Client.HTTPClient`)
- Standard `log.Printf` for all logging, no framework
- `fmt.Errorf("context: %w", err)` for error wrapping

### Integration Points
- `Config` struct in `internal/config/config.go`: add `Transcribe TranscribeConfig` field
- `kapso.Client` in `internal/kapso/client.go`: add `DownloadMedia` method
- `cmd/kapso-whatsapp-poller/main.go`: build Transcriber from config, pass nil if disabled (WIRE-01)

</code_context>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 01-foundation*
*Context gathered: 2026-03-01*
