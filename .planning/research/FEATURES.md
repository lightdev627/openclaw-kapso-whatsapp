# Feature Research

**Domain:** STT integration in a WhatsApp-to-AI-gateway messaging bridge
**Researched:** 2026-03-01
**Confidence:** MEDIUM — core table stakes verified against multiple official API sources; differentiator judgements derive from ecosystem research and community patterns

---

## Feature Landscape

### Table Stakes (Users Expect These)

Features users assume exist. Missing these = product feels incomplete or broken.

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Transcribe OGG/Opus audio | WhatsApp voice notes are always `audio/ogg` with Opus codec; no transcription = no use | LOW | Groq, OpenAI, Deepgram all accept `ogg` natively. whisper.cpp needs WAV — requires ffmpeg conversion. MEDIUM complexity for local path. |
| Configurable provider (cloud or local) | Privacy-sensitive users need offline option; cost-sensitive users need cloud options | LOW | Implemented via `Transcriber` interface + `[transcribe]` config section. Already in scope per PROJECT.md. |
| Language hint passthrough | Non-English WhatsApp users get poor accuracy without it; forces Whisper into auto-detect which adds latency | LOW | All providers accept `language` param (ISO-639-1). Auto-detect works but is slower and less accurate on short clips. |
| Graceful fallback on failure | Transcription errors must not drop messages or break the relay pipeline | LOW | Fall back to `[audio] (mime/type)` and log a warning. Already a named requirement in PROJECT.md. |
| `[voice]` prefix on output | Agents need to distinguish transcribed audio from typed text to respond appropriately | LOW | Simple string prefix. Required by agent skill contract in PROJECT.md. |
| Respect 25 MB size limit | Whisper API (OpenAI and Groq) enforces a 25 MB max; exceeding it returns 413 and breaks silently | LOW | Enforce download size cap. Config: `max_audio_size`. Already in scope. |
| Configurable model per provider | `whisper-1` vs `whisper-large-v3` vs `nova-2` have different cost/latency/accuracy tradeoffs | LOW | Already scoped in PROJECT.md: `TRANSCRIBE_MODEL` env override. |

### Differentiators (Competitive Advantage)

Features that set the integration apart. Not required for correctness, but add meaningful value.

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| Audio content-hash caching | Avoids duplicate API calls when the same voice note is retried or redelivered (WhatsApp webhook retries are common); reduces cost measurably | MEDIUM | SHA-256 the raw audio bytes; cache `hash -> transcript` in memory (map with mutex) with configurable TTL. No external store needed for a single-instance daemon. |
| Configurable retry with exponential backoff | Transient 5xx errors from STT APIs (Groq, OpenAI) are common under load; without retry, transient failures silently degrade to `[audio]` fallback | LOW | Retry on 429 and 5xx. Cap at 3 attempts, base delay 1s, factor 2x, 500ms jitter. Do not retry 4xx. Context-aware: respect `ctx.Done()`. |
| Language auto-detection passthrough | When `language` is empty in config, let the provider detect; expose detected language in log output so operators can tune | LOW | Already implicit in all providers — this is about logging the `detected_language` field from `verbose_json` responses for observability. |
| Verbose response mode for debugging | Log `avg_logprob`, `no_speech_prob`, and detected language from `verbose_json` at DEBUG level; helps operators diagnose poor transcription quality | LOW | Only for Whisper-family providers. Log fields at `debug` level; never expose to OpenClaw pipeline. |
| Transcription quality guard (`no_speech_prob` threshold) | Whisper can hallucinate on silent or music-only clips, producing junk text; a high `no_speech_prob` should fall back to `[audio]` rather than sending garbage | LOW | Read `no_speech_prob` from `verbose_json`. Configurable threshold (e.g., `0.85` default). Only relevant for Whisper providers. |
| Prompt/context injection for Whisper | Whisper's `prompt` param (max 224 tokens) can be used to bias transcription toward domain vocabulary (e.g., "AI assistant conversation"); improves accuracy on domain terms | LOW | Expose as `transcribe_prompt` config key. Optional; defaults to empty. |

### Anti-Features (Commonly Requested, Often Problematic)

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|-----------------|-------------|
| Speaker diarization | Voice notes could have multiple speakers; "who said what" seems useful | WhatsApp voice notes are short (<2 min), single-sender-to-single-receiver by design. Multi-speaker notes are rare. Diarization adds latency, cost, and API complexity. Deepgram charges extra; Whisper does not natively support it. For a relay bridge, the agent does not need speaker labels. | If truly needed in the future, use WhisperX or Deepgram's `diarize=true` as an optional post-processing step. Out of scope now. |
| Profanity filtering / content moderation | Operators may want clean transcripts forwarded to agents | STT-level profanity filtering (asterisks) corrupts the text before the AI agent sees it — the agent cannot understand intent from `f***`. Content policy belongs at the agent level, not the transcription layer. Deepgram's redaction mode exists for PII, not policy enforcement. | Let OpenClaw/agent handle content policy. Pass raw transcript. |
| Real-time streaming STT (word-by-word) | Seems faster; platforms advertise real-time as premium | WhatsApp voice notes are pre-recorded, fully buffered files — not live streams. Streaming STT is for live phone calls, not playback files. Adds WebSocket complexity for zero benefit on batch input. | Use batch/file transcription endpoints only (already the plan). |
| Text-to-speech for replies | Full voice loop; "respond with audio too" | Out of scope per PROJECT.md. TTS adds different API, delivery format, and WhatsApp media sending requirements. Adds complexity without clear user demand at this stage. | Defer to a separate milestone if voice reply UX is validated. |
| On-device model download + auto-update | Bundling whisper models to avoid manual setup | Whisper models are 75 MB–2.9 GB. Shipping them in the binary or auto-downloading at runtime creates distribution, storage, and network problems. The operator choosing local should control model selection. | Document model download procedure in README. Require operator to pre-stage model file; expose `model_path` config. |
| Per-message confidence threshold routing | Route low-confidence transcripts to a human review queue | Adds stateful queue, persistence, and operator UI — far beyond scope of a relay bridge. Confidence is useful for logging and fallback decisions, not routing. | Log `avg_logprob` at debug level. Use `no_speech_prob` guard only. |

---

## Feature Dependencies

```
[Media download with size limit]
    └──required by──> [Audio transcription (all providers)]
                          └──required by──> [voice prefix + pipeline injection]
                                                └──required by──> [OpenClaw receives usable text]

[Transcriber interface]
    └──required by──> [HTTP API providers (OpenAI, Groq, Deepgram)]
    └──required by──> [Local whisper.cpp provider]

[verbose_json response format]
    └──enables──> [no_speech_prob quality guard]
    └──enables──> [language detection logging]
    └──enables──> [avg_logprob debug logging]

[Audio content-hash caching]
    └──enhances──> [Retry with exponential backoff]
    (cache hit avoids the retry entirely)

[Retry with exponential backoff]
    └──enhances──> [Graceful fallback]
    (retries are exhausted before falling back)

[OGG/Opus → WAV conversion (ffmpeg)]
    └──required by──> [Local whisper.cpp provider only]
    (cloud providers accept ogg natively)
```

### Dependency Notes

- **Media download requires size limit enforcement:** All providers reject oversized files. The cap must happen before any provider call, not inside provider implementations.
- **verbose_json enables quality guard:** `no_speech_prob` is only available in `verbose_json` format. Enabling the guard requires parsing the richer response, which adds minor complexity. Default response format (`json`) does not include it.
- **Caching + retry are complementary:** A cache hit means no retry is needed. Retry logic activates on cache miss + API failure. They do not conflict.
- **ffmpeg dependency is local-provider-only:** Cloud providers accept `audio/ogg` natively (confirmed for Groq, OpenAI, and Deepgram). Do not make ffmpeg a hard dependency for the cloud path.

---

## MVP Definition

### Launch With (v1)

Minimum viable for the voice transcription milestone. Correctness and reliability, no extras.

- [ ] `Transcriber` interface — single method: `Transcribe(ctx, audio []byte, mimeType string) (string, error)`
- [ ] OpenAI Whisper HTTP provider — multipart form upload, `language` param, configurable model
- [ ] Groq Whisper HTTP provider — same multipart shape as OpenAI, shared logic where possible
- [ ] Deepgram Nova HTTP provider — binary body + query params, configurable model
- [ ] Local whisper.cpp exec provider — shell out to binary, capture stdout, temp WAV file, ffmpeg for OGG
- [ ] Media download on Kapso client — enforce 25 MB cap, return `[]byte` + MIME type
- [ ] Extract integration — audio message detected → download → transcribe → `[voice] ` prefix → normal relay pipeline
- [ ] Graceful degradation — transcription error → `[audio] (mime)` fallback + log warning
- [ ] Config section `[transcribe]` with provider, api_key, model, language, max_audio_size
- [ ] Env overrides: `TRANSCRIBE_PROVIDER`, `TRANSCRIBE_API_KEY`, `TRANSCRIBE_MODEL`, `TRANSCRIBE_LANGUAGE`
- [ ] Table-driven tests for each provider, media download, and extract integration path

### Add After Validation (v1.x)

Add once v1 ships and production audio quality is observable.

- [ ] Retry with exponential backoff — add once operators report transient failures in production; implement as a `retrying` wrapper around any `Transcriber`
- [ ] `no_speech_prob` quality guard — add once hallucination on silent clips is observed; requires switching to `verbose_json` response format for Whisper providers
- [ ] Audio content-hash caching — add if webhook retries cause duplicate STT billing; simple in-memory map sufficient for a single daemon instance
- [ ] Debug-level logging of `avg_logprob` and detected language — low cost to add, high operator value

### Future Consideration (v2+)

Defer until product-market fit with voice messages is confirmed.

- [ ] Whisper prompt/context injection — useful for domain-specific accuracy but requires operator tuning effort
- [ ] Video note audio track extraction — separate milestone; requires different media type handling
- [ ] Speaker diarization — only if group voice notes become a real use case
- [ ] Text-to-speech for replies — separate full milestone, not an extension of this one

---

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|---------------------|----------|
| Transcribe OGG/Opus via cloud provider | HIGH | LOW | P1 |
| Graceful fallback on failure | HIGH | LOW | P1 |
| `[voice]` prefix | HIGH | LOW | P1 |
| Media download with size cap | HIGH | LOW | P1 |
| Configurable provider + model + language | HIGH | LOW | P1 |
| Local whisper.cpp provider | MEDIUM | MEDIUM | P1 (privacy use case is strong) |
| Retry with exponential backoff | HIGH | LOW | P2 |
| `no_speech_prob` quality guard | MEDIUM | LOW | P2 |
| Audio content-hash caching | MEDIUM | LOW | P2 |
| Debug logging (avg_logprob, detected lang) | LOW | LOW | P2 |
| Whisper prompt injection | LOW | LOW | P3 |
| Speaker diarization | LOW | HIGH | P3 (defer) |
| TTS for replies | LOW | HIGH | P3 (defer) |

**Priority key:**
- P1: Must have for launch milestone
- P2: Should have, add post-launch when production evidence supports it
- P3: Nice to have, future consideration

---

## Competitor Feature Analysis

WhatsApp voice transcription in similar bridges and comparable messaging bots:

| Feature | n8n Whisper+Groq workflow | IBM Watson WhatsApp bot | Our Approach |
|---------|--------------------------|-------------------------|--------------|
| Provider | Groq Whisper (single) | Watson STT (single) | Pluggable: OpenAI / Groq / Deepgram / local |
| Fallback on error | Not documented | Not documented | `[audio] (mime)` fallback, log warning |
| Language support | Auto-detect | Fixed per config | Config + auto-detect per provider |
| Local/offline option | No | No | Yes (whisper.cpp exec) |
| Retry logic | No (n8n handles externally) | No | To be added post-v1 |
| Caching | No | No | In-memory SHA-256 cache (post-v1) |
| Prefix marking | Depends on workflow | Depends on bot logic | `[voice] ` prefix, always applied |

---

## Sources

- [Groq Speech-to-Text docs](https://console.groq.com/docs/speech-to-text) — confirmed OGG support, language param, verbose_json format, model list (HIGH confidence)
- [Groq Whisper Large v3 Turbo](https://groq.com/blog/whisper-large-v3-turbo-now-available-on-groq-combining-speed-quality-for-speech-recognition) — 216x real-time factor, $0.04/hr pricing (HIGH confidence)
- [OpenAI Whisper GitHub](https://github.com/openai/whisper) — avg_logprob, language detection, no_speech_prob from segment output (HIGH confidence)
- [whisper.cpp GitHub](https://github.com/ggml-org/whisper.cpp) — local exec, model sizes, OGG needs WAV conversion (HIGH confidence)
- [Deepgram STT product page](https://deepgram.com/product/speech-to-text) — Nova-3, smart formatting, diarization, 45+ languages (MEDIUM confidence — official marketing page)
- [Deepgram getting started](https://developers.deepgram.com/docs/stt/getting-started) — batch vs streaming confirmation (MEDIUM confidence)
- [n8n Groq Whisper WhatsApp workflow](https://n8n.io/workflows/6077-transcribe-whatsapp-audio-messages-with-whisper-ai-via-groq/) — confirms OGG WhatsApp → Groq transcription pattern in production use (MEDIUM confidence)
- [AssemblyAI retry guidance](https://www.assemblyai.com/blog/customer-issues-retrying-requests) — retry on 5xx, exponential backoff pattern (MEDIUM confidence)
- [WhatsApp OGG/Opus format confirmation](https://github.com/aldinokemal/go-whatsapp-web-multidevice/issues/501) — OGG/Opus delivery issues and format requirements (MEDIUM confidence)
- [WhatsApp supported media types](https://www.chatondesk.com/supported-media-types-in-whatsapp/) — confirmed audio/ogg as voice note format (MEDIUM confidence)
- [Speaker diarization 2025 guide — AssemblyAI](https://www.assemblyai.com/blog/top-speaker-diarization-libraries-and-apis) — diarization practical value in enterprise, but for call centers/meetings not one-to-one messaging (MEDIUM confidence)
- [STT architecture best practices — AIMLAPI](https://aimlapi.com/blog/introduction-to-speech-to-text-technology) — caching, deduplication, no_speech_prob threshold (MEDIUM confidence)

---

*Feature research for: STT integration in WhatsApp-OpenClaw bridge*
*Researched: 2026-03-01*
