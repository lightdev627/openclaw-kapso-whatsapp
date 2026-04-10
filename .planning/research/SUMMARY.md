# Project Research Summary

**Project:** kapso-whatsapp — Voice Transcription (STT) Integration
**Domain:** Speech-to-text provider integration in a Go WhatsApp-to-AI-gateway bridge
**Researched:** 2026-03-01
**Confidence:** MEDIUM-HIGH

## Executive Summary

This milestone adds voice transcription to the existing Go daemon that bridges WhatsApp (via Kapso Cloud API) to OpenClaw AI. The integration intercepts audio messages, downloads the OGG/Opus audio file, transcribes it via a configurable provider (Groq, OpenAI, Deepgram, or local whisper.cpp), prepends a `[voice]` prefix, and injects the text into the existing relay pipeline. The entire new surface area is a single new package (`internal/transcribe/`) and two narrow signature changes to existing code. No new Go module dependencies are required for any cloud provider implementation.

The recommended default provider is Groq Whisper (`whisper-large-v3-turbo`) — it delivers 216x real-time speed at $0.04/audio-hour, accepts OGG/Opus natively, and its API shape is identical to OpenAI, meaning one shared implementation covers two providers via a configurable `BaseURL`. The local whisper.cpp provider (via `os/exec` + ffmpeg) supports privacy-first and air-gapped deployments but adds two external binary dependencies and 5–60s latency per clip. Deepgram Nova-3 is the right choice for multilingual, accuracy-critical workloads.

The most dangerous failure modes are all related to WhatsApp's media URL expiry (5 minutes), MIME type mismatch between `audio/ogg; codecs=opus` and what providers accept, and pipeline blocking caused by synchronous transcription without a context deadline. All three are deterministic bugs that produce intermittent production failures with no obvious error. The architecture mitigates them by: downloading audio immediately at the URL retrieval site, normalising MIME types via a shared helper, and wrapping every `Transcribe()` call in `context.WithTimeout`.

## Key Findings

### Recommended Stack

The implementation is pure Go stdlib — zero new module dependencies. OpenAI and Groq both use `multipart/form-data` POST bodies (identical shape, different `BaseURL`), handled with `mime/multipart`. Deepgram uses a binary body POST with query parameters, handled with `bytes.NewReader` and `net/url`. The local provider shells out to `whisper-cli` and `ffmpeg` via `os/exec`; CGO bindings are explicitly ruled out because CGO is disabled project-wide.

**Core technologies:**
- `net/http` + `mime/multipart` (stdlib): HTTP provider calls — no SDKs, project convention
- `os/exec` + `os.MkdirTemp` (stdlib): local whisper.cpp provider — CGO disabled, shell-out is the only viable path
- `ffmpeg` (external binary, local provider only): OGG/Opus to 16kHz mono WAV conversion required by whisper.cpp
- `whisper-cli` (external binary, local provider only): offline transcription — build from ggml-org/whisper.cpp

**Provider defaults:**
- Groq `whisper-large-v3-turbo` — recommended default (fastest, cheapest, OGG native)
- OpenAI `whisper-1` — drop-in if already using OpenAI credentials
- Deepgram `nova-3` — best OGG/Opus support, best multilingual accuracy
- Local whisper.cpp — privacy-first, no API key, high latency acceptable

See `.planning/research/STACK.md` for full provider API reference, Go code patterns, and version compatibility matrix.

### Expected Features

The feature set is tightly scoped. Everything in v1 can be built without any new Go dependencies. Post-v1 improvements are additive wrappers around the `Transcriber` interface.

**Must have (table stakes):**
- Transcribe OGG/Opus audio via any configured provider — core value delivery
- Configurable provider, model, and language via `[transcribe]` TOML section + env vars
- `[voice]` prefix on transcribed output — agent skill contract requires it
- Graceful fallback to `[audio] (mime)` on failure — message pipeline must never block
- 25 MB audio size cap enforced during download — all cloud providers enforce this limit
- Language hint passthrough — non-English accuracy degrades severely without it
- Table-driven tests for every provider and the integration path

**Should have (post-v1 when production evidence supports it):**
- Retry with exponential backoff (3 attempts, 1s base, 2x factor) — transient 5xx errors are common
- `no_speech_prob` quality guard — prevents Whisper hallucinating on silent clips
- Audio content-hash caching (in-memory SHA-256) — avoids duplicate API billing on webhook retries
- Debug-level logging of `avg_logprob` and detected language — operator observability

**Defer (v2+):**
- Whisper prompt/context injection — requires operator tuning, low ROI at v1
- Video note audio track extraction — separate media type, separate milestone
- Speaker diarization — no real use case in one-to-one WhatsApp messaging
- Text-to-speech for replies — entirely separate feature, different delivery requirements

See `.planning/research/FEATURES.md` for full prioritisation matrix and competitor analysis.

### Architecture Approach

The new transcription layer is minimally invasive: one new package (`internal/transcribe/`), one new method on the existing Kapso client (`DownloadMedia`), one widened function signature in `delivery.ExtractText`, and one wiring call in `main.go`. All data flow is downward — no circular dependencies are introduced. The `Transcriber` interface is defined at the consumer (`delivery`), not in the implementation package, following idiomatic Go interface placement.

**Major components:**
1. `internal/transcribe/` — `Transcriber` interface, four provider implementations, `New(cfg)` factory; self-contained package, no imports back into delivery or kapso
2. `kapso.Client.DownloadMedia` — fetches raw audio bytes with size cap; shares the existing HTTP client's connection pool
3. `delivery.ExtractText(msg, client, tr)` — gains a `Transcriber` parameter (nil = disabled); audio branch: download, transcribe, prefix, fallback on any error
4. `config.TranscribeConfig` — new `[transcribe]` TOML section: provider, api_key, model, language, max_audio_size; env overrides: `TRANSCRIBE_PROVIDER`, `TRANSCRIBE_API_KEY`, `TRANSCRIBE_MODEL`, `TRANSCRIBE_LANGUAGE`
5. `main.go` wiring — single call to `transcribe.New(cfg.Transcribe)` at startup; nil transcriber means zero behaviour change for existing deployments

**Build order (dependency-constrained):** config struct → Transcriber interface + factory → DownloadMedia → HTTP providers (parallel) + LocalWhisper (parallel) → ExtractText signature change → main.go wiring.

See `.planning/research/ARCHITECTURE.md` for full data flow diagrams, code patterns, anti-patterns, and scaling considerations.

### Critical Pitfalls

1. **Media URL expiry (5-minute window)** — Download audio immediately after `GetMediaURL()`, in the same call path, before returning. Never store or pass a signed URL for later use. Add `context.WithTimeout` of 4 minutes max on the download.

2. **OGG/Opus MIME type mismatch** — Normalise all MIME variants (`audio/ogg; codecs=opus`, `audio/ogg`, `audio/opus`) to `audio/ogg` before sending to any provider. Use `CreatePart()` with explicit `textproto.MIMEHeader`, not `CreateFormFile()` (which hardcodes `application/octet-stream`). Always set the multipart filename to `audio.ogg`, not `audio.opus`.

3. **Pipeline blocking during transcription** — Wrap every `Transcribe()` call with `context.WithTimeout` (30s total for download + transcription). On timeout or error, fall back immediately. Never spawn untracked goroutines for transcription.

4. **Temp file leaks from whisper.cpp subprocess** — Use `exec.CommandContext(ctx, ...)` (never `exec.Command()`), `os.MkdirTemp` for unique per-call directories, and `defer os.RemoveAll(dir)` immediately after `MkdirTemp`. Use `cmd.CombinedOutput()`, not `cmd.Stdout = &bytes.Buffer{}` with pipes.

5. **Silent error masking via fallback** — Always log transcription failures at WARN level. Track consecutive failure count; after 3, emit an ERROR log. Test that errors propagate from `Transcribe()` to the caller — never swallow them inside the implementation.

See `.planning/research/PITFALLS.md` for the full checklist, integration gotchas, performance traps, and security mistakes.

## Implications for Roadmap

The architecture's explicit build order and the pitfall-to-phase mapping from research combine to suggest a natural 4-phase structure.

### Phase 1: Foundation — Config and Interface

**Rationale:** Config and the interface define the contract for everything else. No other component can be built or tested without them. Zero risk of rework: these have no external dependencies.
**Delivers:** `config.TranscribeConfig` struct with TOML parsing and env overrides; `transcribe.Transcriber` interface; `transcribe.New(cfg)` factory stub; config-level validation that rejects unknown provider strings at startup.
**Addresses:** Configurable provider (table stakes), env override requirements
**Avoids:** Pitfall 5 (silent fallback) — define in interface contract that errors must be returned, not swallowed; test this from day one

### Phase 2: Kapso Media Download

**Rationale:** All providers need audio bytes. `DownloadMedia` is a self-contained addition to the existing Kapso client, testable against a mock HTTP server, with no dependency on any provider implementation.
**Delivers:** `kapso.Client.DownloadMedia(ctx, url, maxBytes) ([]byte, error)` with streaming size enforcement via `io.LimitReader`; unit tests including oversized response rejection
**Addresses:** 25 MB size cap (table stakes)
**Avoids:** Pitfall 1 (URL expiry) — document and enforce download-on-retrieval as the API contract of this method; Pitfall 4 variant (memory exhaustion from buffering) — size limit enforced during streaming, not after

### Phase 3: Cloud Provider Implementations

**Rationale:** OpenAI and Groq share one implementation (different `BaseURL`); Deepgram is a separate but simple binary-body implementation. All three can be built in parallel against mock HTTP servers. This phase delivers the primary user value.
**Delivers:** `transcribe.OpenAIWhisper` (covers Groq via `BaseURL`), `transcribe.Deepgram`, table-driven tests for each, MIME normalisation helper shared by all providers
**Uses:** `mime/multipart`, `bytes.NewReader`, `net/url`, `net/http` (all stdlib)
**Avoids:** Pitfall 2 (MIME mismatch) — MIME normalisation helper built here, used by all providers; use `CreatePart()` not `CreateFormFile()`

### Phase 4: Local Provider (whisper.cpp)

**Rationale:** Local provider has unique external dependencies (ffmpeg, whisper-cli) and a distinct failure mode (subprocess lifecycle, temp file leaks). Isolated in its own phase to contain risk and allow cloud providers to ship independently.
**Delivers:** `transcribe.LocalWhisper` with `exec.CommandContext`, `os.MkdirTemp`, ffmpeg OGG-to-WAV conversion, and `defer os.RemoveAll`; tests for context cancellation and temp file cleanup
**Avoids:** Pitfall 4 (temp file leaks) — `exec.CommandContext` + `MkdirTemp` + `RemoveAll` enforced by tests

### Phase 5: Extract Integration and End-to-End

**Rationale:** Final integration. Widens `delivery.ExtractText` to accept a `Transcriber`, connects `DownloadMedia` to `Transcribe`, adds the `[voice]` prefix, implements the fallback, and wires everything in `main.go`. This is the most important correctness phase.
**Delivers:** Modified `delivery.ExtractText(msg, client, tr)` with audio branch; `[voice]` prefix; `[audio] (mime)` fallback with WARN log; `context.WithTimeout` per transcription call; `main.go` wiring; end-to-end integration tests
**Avoids:** Pitfall 3 (pipeline blocking) — context deadline baked into ExtractText, not left as caller responsibility; Pitfall 5 (silent fallback) — WARN log assertion in tests

### Phase 6: Reliability Enhancements (Post-v1)

**Rationale:** Add once production audio traffic reveals actual failure patterns. These are additive wrappers — no changes to the interface or providers already shipped.
**Delivers:** `retrying.Transcriber` wrapper (exponential backoff, 3 attempts, context-aware); `caching.Transcriber` wrapper (SHA-256 content hash, in-memory map, configurable TTL); `verbose_json` mode with `no_speech_prob` quality guard and `avg_logprob` debug logging
**Addresses:** Retry (should-have), no_speech_prob guard (should-have), caching (should-have)

### Phase Ordering Rationale

- Phases 1–2 are dependency gates: nothing else can be built without the interface and the media download.
- Phase 3 delivers end-user value fastest and can be shipped independently as a partial release.
- Phase 4 is isolated because its failure modes (subprocess, external binaries) are fundamentally different from HTTP providers.
- Phase 5 is last in the core milestone because it integrates all prior phases — attempting it earlier would require constant rework as dependencies are added.
- Phase 6 is explicitly post-v1 to avoid over-engineering before production evidence exists.

### Research Flags

Phases likely needing `/gsd:research-phase` during planning:
- **Phase 3 (Cloud Providers):** Integration details for Deepgram's binary body response format (`results.channels[0].alternatives[0].transcript` nesting) and Groq's rate limit behaviour under load are MEDIUM confidence — worth a targeted API validation before implementation.
- **Phase 4 (Local Provider):** whisper.cpp CLI flag stability and the `--output-txt` vs stdout behaviour has MEDIUM confidence from a single community source; validate against the installed binary version before writing the implementation.

Phases with well-documented patterns (skip research-phase):
- **Phase 1 (Config/Interface):** Standard Go config pattern identical to existing `internal/config` — no research needed.
- **Phase 2 (Media Download):** Standard `net/http` streaming download with `io.LimitReader` — well-documented stdlib patterns, HIGH confidence.
- **Phase 5 (Integration):** Pattern is fully specified in ARCHITECTURE.md with code examples; no research needed.
- **Phase 6 (Reliability):** Retry and caching wrapper patterns are well-documented Go idioms.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | MEDIUM-HIGH | OpenAI and Groq endpoints confirmed via community sources and official docs pages (some returned 403/404 on direct fetch, downgrading from HIGH). Deepgram pre-recorded endpoint confirmed via successful WebFetch of official docs. stdlib patterns are HIGH confidence from official Go docs. |
| Features | MEDIUM | Core table stakes verified against official API docs and multiple community sources. Differentiator judgements (retry thresholds, cache TTL) are engineering judgements, not verified norms. |
| Architecture | HIGH | Codebase read directly; patterns verified against official Go docs and provider API specifications. Component boundaries and data flow are well-understood. |
| Pitfalls | HIGH | Most pitfalls verified via official GitHub issues, community bug reports, and documented API limitations. The 5-minute media URL expiry is documented by Vonage (WhatsApp partner). The `CreateFormFile` hardcoding is documented in the official Go stdlib. |

**Overall confidence:** MEDIUM-HIGH

### Gaps to Address

- **Groq rate limits under load:** The specific audio-seconds-per-minute rate limit for Groq's free tier is documented as existing but exact numbers require live verification at `console.groq.com/settings/limits`. Plan for 429 handling in Phase 3 without knowing the exact threshold.
- **whisper.cpp `--output-txt` flag stability:** Confirmed in multiple sources but from a single Applied Go article for the Go exec pattern. Validate the exact CLI flags against the installed binary version before implementing Phase 4.
- **Deepgram response format depth:** The nested `results.channels[0].alternatives[0].transcript` path was confirmed but only through a partially-loaded docs page. Add a dedicated integration test with a real (or recorded) Deepgram response before shipping Phase 3.
- **Kapso `AudioContent` struct fields:** The exact field names for audio message content (`.ID`, `.MimeType`) on the `kapso.Message` struct were inferred from the existing codebase structure. Verify against actual Kapso webhook payloads before implementing Phase 5's audio branch.

## Sources

### Primary (HIGH confidence)
- Go `net/http`, `mime/multipart`, `os`, `context` stdlib docs — Go patterns for HTTP clients, multipart uploads, temp files
- OpenAI Whisper GitHub (`github.com/openai/whisper`) — response field names, `no_speech_prob`, `avg_logprob`
- whisper.cpp GitHub (`github.com/ggml-org/whisper.cpp`) — CLI flags, WAV requirement, model files, temp file collision issue #2327
- Deepgram pre-recorded audio docs (`developers.deepgram.com/docs/pre-recorded-audio`) — binary body pattern, query params
- ffmpeg OGG/Opus to WAV conversion flags — from whisper.cpp official documentation

### Secondary (MEDIUM confidence)
- Groq Speech-to-Text docs (`console.groq.com/docs/speech-to-text`) — endpoint, OGG support, language param, rate limits (direct page returned 404; confirmed via WebSearch summary)
- OpenAI Whisper API reference — multipart fields, OGG support, model names (direct page returned 403; confirmed via community sources)
- Vonage API Support — WhatsApp media URL 5-minute expiry window
- Applied Go whisper.cpp exec pattern (`appliedgo.net/whisper-cli/`) — Go subprocess pattern for whisper.cpp CLI
- n8n Groq Whisper WhatsApp workflow — confirms OGG WhatsApp to Groq transcription in production use
- AssemblyAI retry guidance — exponential backoff on 5xx, context-aware retry patterns
- whisper.cpp issue #2327 — temp file collision with static names in concurrent usage

### Tertiary (MEDIUM-LOW confidence)
- Deepgram Nova-3 announcement — model accuracy claims (marketing page)
- STT architecture best practices (AIMLAPI blog) — caching, deduplication, `no_speech_prob` threshold recommendations

---
*Research completed: 2026-03-01*
*Ready for roadmap: yes*
