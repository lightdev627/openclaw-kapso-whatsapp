# Stack Research

**Domain:** STT provider integration in a Go WhatsApp-to-AI-gateway bridge
**Researched:** 2026-03-01
**Confidence:** MEDIUM-HIGH (API endpoints verified via official docs and Go community sources; some endpoint details confirmed via WebSearch + multiple agreeing sources rather than direct doc access)

---

## Recommended Stack

### Core Technologies

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| Go stdlib `net/http` | Go 1.22 (existing) | All HTTP calls to STT providers | Project convention: no SDKs. `http.Client` is concurrent-safe, has connection pooling, handles keep-alive automatically. Every STT provider exposes a REST endpoint; no SDK is needed. |
| Go stdlib `mime/multipart` | Go 1.22 (existing) | Build `multipart/form-data` request bodies for OpenAI and Groq | OpenAI and Groq require multipart. Stdlib `multipart.Writer` handles boundary generation correctly. Zero new dependency. |
| Go stdlib `os/exec` | Go 1.22 (existing) | Fork whisper.cpp and ffmpeg subprocesses for local provider | CGO is disabled (project constraint). The whisper.cpp Go bindings (`github.com/ggml-org/whisper.cpp/bindings/go`) link `libwhisper.a` and require CGO. Shell-out is the only viable local path. |
| Go stdlib `os.MkdirTemp` | Go 1.22 (existing) | Unique temp directory per local transcription call | Prevents temp file collisions when multiple audio messages are processed concurrently. See whisper.cpp issue #2327 for the race condition this prevents. |

No new Go module dependencies are required for any HTTP provider. The local whisper provider requires two **external binaries** on the host — see below.

---

### External Binaries (Local Provider Only)

| Binary | Version | Purpose | Notes |
|--------|---------|---------|-------|
| `whisper-cli` (whisper.cpp) | Latest stable from [ggml-org/whisper.cpp](https://github.com/ggml-org/whisper.cpp) | Offline transcription binary | Must be built with `WHISPER_FFMPEG=ON` to accept OGG/Opus directly, OR ffmpeg handles conversion first. Configured via `whisper_binary_path` in TOML. |
| `ffmpeg` | 6.x or later (distro package) | Convert OGG/Opus to 16 kHz mono WAV before whisper.cpp | Required because whisper.cpp's native input is 16-bit WAV at 16 kHz. WhatsApp audio arrives as `audio/ogg; codecs=opus`. Only needed for local provider. |
| whisper.cpp model file | `ggml-*.bin` (e.g., `ggml-base.en.bin`) | Language model weights | Downloaded separately from Hugging Face or whisper.cpp releases. Configured via `whisper_model_path` in TOML. Size: 75 MB (base.en) to 2.9 GB (large-v3). |

---

### Supporting Libraries (No New Go Deps)

The entire implementation uses only Go stdlib packages already present in the module graph:

| Package | Purpose |
|---------|---------|
| `bytes` | Buffer for multipart body construction |
| `context` | Deadline/cancellation propagation through DownloadMedia and Transcribe |
| `encoding/json` | Decode JSON responses from all HTTP providers |
| `fmt` | Error wrapping with `%w` |
| `mime/multipart` | Build multipart/form-data bodies (OpenAI, Groq) |
| `net/http` | All outbound HTTP calls |
| `net/url` | Build query strings for Deepgram (`url.Values`) |
| `os` | Temp file creation, reading transcript output |
| `os/exec` | Fork whisper.cpp and ffmpeg subprocesses |
| `path/filepath` | Construct temp file paths safely |
| `strings` | Trim whitespace from whisper.cpp transcript output |

---

## Provider Reference

### Provider 1: Groq Whisper (RECOMMENDED default)

**Why lead with Groq:** 216x real-time speed factor on their LPU hardware. Pricing at $0.04/hour is the lowest of the cloud providers. API is structurally identical to OpenAI, so it shares the same Go implementation. Rate limits are generous for a single WhatsApp bridge deployment.

**Confidence:** MEDIUM (endpoint verified via multiple community sources and official Groq docs page; direct doc fetch returned 404 so not HIGH)

| Property | Value |
|----------|-------|
| **Endpoint** | `POST https://api.groq.com/openai/v1/audio/transcriptions` |
| **Auth** | `Authorization: Bearer <GROQ_API_KEY>` |
| **Request body** | `multipart/form-data` |
| **Multipart fields** | `file` (binary audio bytes), `model` (string), optional `language` (ISO-639-1) |
| **Recommended model** | `whisper-large-v3-turbo` (fastest); `whisper-large-v3` (highest accuracy) |
| **OGG/Opus support** | YES — `ogg` is in Groq's supported formats list (same as OpenAI) |
| **File size limit** | 25 MB |
| **Response JSON** | `{"text": "...", "x_groq": {...}}` — extract `.text` |
| **Response format param** | `json` (default), `text`, `verbose_json`; NOT `vtt` or `srt` (unsupported by Groq) |

**Go auth header:**
```go
req.Header.Set("Authorization", "Bearer "+apiKey)
```

**Note on `.opus` vs `.ogg`:** WhatsApp delivers audio as `audio/ogg; codecs=opus`. The file is an OGG container (not bare `.opus`). Groq accepts `ogg` as a format, so the file extension passed as the `file` field filename should be `.ogg`. The binary content is sent as-is; no conversion needed for cloud providers.

---

### Provider 2: OpenAI Whisper

**Why:** Widest adoption, most tested. `whisper-1` (Whisper v2) has been stable for years. New `gpt-4o-transcribe` and `gpt-4o-mini-transcribe` models are available but response format is restricted to `json` only and they are priced higher. Use `whisper-1` for cost-effectiveness.

**Confidence:** MEDIUM (endpoint verified via OpenAI API reference URL and community sources; direct doc page returned 403)

| Property | Value |
|----------|-------|
| **Endpoint** | `POST https://api.openai.com/v1/audio/transcriptions` |
| **Auth** | `Authorization: Bearer <OPENAI_API_KEY>` |
| **Request body** | `multipart/form-data` |
| **Multipart fields** | `file` (binary audio bytes, filename matters for format detection), `model` (string), optional `language` (ISO-639-1), optional `response_format` |
| **Recommended model** | `whisper-1` for cost; `gpt-4o-mini-transcribe` for higher accuracy (JSON-only response) |
| **OGG/Opus support** | YES for `.ogg` container. CAUTION: bare `.opus` (no OGG container) is NOT in the official supported list and may fail. WhatsApp's `audio/ogg; codecs=opus` is OGG-containerized, so it works. Pass filename as `audio.ogg`. |
| **File size limit** | 25 MB |
| **Officially supported formats** | `flac, mp3, mp4, mpeg, mpga, m4a, ogg, wav, webm` |
| **Response JSON** | `{"text": "..."}` — extract `.text` |

**Go implementation is identical to Groq** — only the base URL differs. Use one struct with a configurable `BaseURL`:

```go
type OpenAIWhisper struct {
    BaseURL    string       // "https://api.openai.com/v1" or "https://api.groq.com/openai/v1"
    APIKey     string
    Model      string       // "whisper-1" for OpenAI, "whisper-large-v3-turbo" for Groq
    Language   string       // optional, ISO-639-1
    HTTPClient *http.Client // nil = http.DefaultClient
}
```

---

### Provider 3: Deepgram Nova

**Why:** Binary body request (simpler than multipart). Nova-3 is Deepgram's latest model with 54% WER reduction vs competitors. Deepgram explicitly supports OGG and Opus as containerized audio — the best OGG/Opus support of the three cloud providers.

**Confidence:** MEDIUM (endpoint verified via official Deepgram docs page successfully fetched; model names from WebSearch and Deepgram blog)

| Property | Value |
|----------|-------|
| **Endpoint** | `POST https://api.deepgram.com/v1/listen` |
| **Auth** | `Authorization: Token <DEEPGRAM_API_KEY>` (NOT "Bearer" — "Token") |
| **Request body** | Raw binary audio bytes — NOT multipart |
| **Content-Type header** | Must be the audio MIME type: `audio/ogg; codecs=opus` for WhatsApp audio |
| **Model selection** | Query parameter: `?model=nova-3` |
| **Language selection** | Query parameter: `&language=en` (optional) |
| **Recommended model** | `nova-3` (latest, best accuracy); `nova-2` (stable alternative) |
| **OGG/Opus support** | YES — explicitly listed. OGG container with Opus codec is natively supported; omit `encoding` and `sample_rate` params for containerized audio. |
| **File size limit** | Not explicitly documented (uses time-based limits: up to 10 min for Nova models) |
| **Response JSON** | `results.channels[0].alternatives[0].transcript` |

**Go auth header (different from OpenAI/Groq):**
```go
req.Header.Set("Authorization", "Token "+apiKey)  // "Token", not "Bearer"
```

**Go request construction (binary body, no multipart):**
```go
req, err := http.NewRequestWithContext(ctx, http.MethodPost,
    "https://api.deepgram.com/v1/listen?"+q.Encode(),
    bytes.NewReader(audio))
req.Header.Set("Authorization", "Token "+apiKey)
req.Header.Set("Content-Type", mimeType) // pass through the MIME type from kapso.Message
```

**Deepgram response JSON extraction:**
```go
var resp struct {
    Results struct {
        Channels []struct {
            Alternatives []struct {
                Transcript string `json:"transcript"`
            } `json:"alternatives"`
        } `json:"channels"`
    } `json:"results"`
}
// extract: resp.Results.Channels[0].Alternatives[0].Transcript
```

---

### Provider 4: Local whisper.cpp

**Why:** Privacy-first, air-gapped deployments. No data leaves the host. No API key required. Suitable for single-user, low-volume deployments where latency (5–30s per clip) is acceptable.

**Confidence:** MEDIUM (whisper.cpp CLI flags and WAV requirement verified via official GitHub repo; Go exec pattern from Applied Go article, consistent with stdlib docs)

| Property | Value |
|----------|-------|
| **Binary** | `whisper-cli` (or legacy `main`) from ggml-org/whisper.cpp |
| **Input format** | 16-bit WAV at 16 kHz mono (required). OGG/Opus must be converted first via ffmpeg. Alternatively, build whisper.cpp with `WHISPER_FFMPEG=ON` to skip conversion. |
| **ffmpeg conversion command** | `ffmpeg -i input.ogg -ar 16000 -ac 1 -c:a pcm_s16le output.wav` |
| **Key CLI flags** | `--model <path>`, `--output-txt` (writes `<input>.txt`), `--no-progress`, optional `--language <lang>` |
| **Transcript output** | Written to `<inputfile>.txt` alongside the WAV file; NOT to stdout |
| **Concurrent safety** | Use `os.MkdirTemp("", "kapso-whisper-*")` — unique dir per call. Never use static temp file names. |
| **CGO** | NOT used. Shell-out via `os/exec` only. CGO bindings exist but violate project constraint. |

**Go subprocess pattern:**
```go
dir, _ := os.MkdirTemp("", "kapso-whisper-*")
defer os.RemoveAll(dir)

// 1. Write raw audio
rawPath := filepath.Join(dir, "audio.ogg")
os.WriteFile(rawPath, audio, 0o600)

// 2. Convert to WAV via ffmpeg
wavPath := filepath.Join(dir, "audio.wav")
cmd := exec.CommandContext(ctx, "ffmpeg",
    "-i", rawPath,
    "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le",
    wavPath)
out, err := cmd.CombinedOutput()
// wrap error: fmt.Errorf("ffmpeg: %w: %s", err, out)

// 3. Run whisper.cpp
cmd = exec.CommandContext(ctx, binaryPath,
    "--model", modelPath,
    "--output-txt",
    "--no-progress",
    wavPath)
out, err = cmd.CombinedOutput()
// wrap error: fmt.Errorf("whisper: %w: %s", err, out)

// 4. Read transcript
raw, _ := os.ReadFile(wavPath + ".txt")
return strings.TrimSpace(string(raw)), nil
```

---

## WhatsApp Audio Format

This is the critical format to get right. Every provider decision about OGG/Opus support flows from this.

| Property | Value | Confidence |
|----------|-------|------------|
| **Container** | OGG | HIGH (official WhatsApp docs + multiple sources) |
| **Codec** | Opus | HIGH |
| **MIME type delivered by Kapso API** | `audio/ogg; codecs=opus` | HIGH (WhatsApp Cloud API spec) |
| **File extension** | `.ogg` | HIGH |
| **Sample rate** | 48,000 Hz | MEDIUM (commonly cited, standard for Opus) |
| **Channels** | Mono (1) | MEDIUM |
| **Bitrate** | ~32 kbps | MEDIUM |
| **Maximum duration** (WhatsApp limit) | ~16 minutes (practical: voice notes are <2 min) | MEDIUM |
| **Maximum file size** (Kapso API) | Not documented; recommend capping download at 25 MB (matches all cloud provider limits) | MEDIUM |

**Why `.opus` bare format is NOT the same as `.ogg` with Opus:** WhatsApp sends an OGG _container_ file with Opus-encoded audio inside. This is `audio/ogg; codecs=opus`. The MIME type `audio/opus` (bare Opus, no container) is different and rejected by the WhatsApp Cloud API itself when sending. All STT providers accept the OGG-containerized form.

**Recommendation:** Pass the MIME type from `kapso.Message.Audio.MimeType` directly through to the Transcriber interface. Do not hardcode `audio/ogg`. This handles future format changes and avoids a mismatch between what Kapso reports and what is sent to the provider.

---

## Go HTTP Patterns

### Multipart Upload (OpenAI / Groq)

```go
// mime/multipart + bytes — stdlib only
var buf bytes.Buffer
w := multipart.NewWriter(&buf)

// CreateFormFile uses Content-Disposition: form-data; name="file"; filename="audio.ogg"
// It defaults Content-Type to application/octet-stream.
// For Whisper APIs, that is fine — they detect format from file content, not MIME.
fw, err := w.CreateFormFile("file", "audio.ogg")
if err != nil {
    return "", fmt.Errorf("create form file: %w", err)
}
if _, err := fw.Write(audio); err != nil {
    return "", fmt.Errorf("write audio: %w", err)
}
_ = w.WriteField("model", model)
if language != "" {
    _ = w.WriteField("language", language)
}
w.Close() // writes multipart trailing boundary — required

req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
req.Header.Set("Authorization", "Bearer "+apiKey)
req.Header.Set("Content-Type", w.FormDataContentType()) // includes boundary
```

**Note on `CreateFormFile` vs `CreatePart`:** `w.CreateFormFile("file", "audio.ogg")` is sufficient for Whisper APIs. They detect audio format from the file content (via ffmpeg internally), not the part Content-Type. Only use `CreatePart` with a custom `textproto.MIMEHeader` if a specific `Content-Type` per-part is required — it is not for any of the target providers.

### Binary Body Upload (Deepgram)

```go
// Simpler than multipart — just bytes.NewReader
q := url.Values{}
q.Set("model", model)
if language != "" {
    q.Set("language", language)
}
req, err := http.NewRequestWithContext(ctx, http.MethodPost,
    "https://api.deepgram.com/v1/listen?"+q.Encode(),
    bytes.NewReader(audio))
req.Header.Set("Authorization", "Token "+apiKey) // "Token" not "Bearer"
req.Header.Set("Content-Type", mimeType)          // e.g. "audio/ogg; codecs=opus"
```

### Response Reading Pattern (all HTTP providers)

```go
resp, err := httpClient.Do(req)
if err != nil {
    return "", fmt.Errorf("http do: %w", err)
}
defer resp.Body.Close()

body, err := io.ReadAll(resp.Body)
if err != nil {
    return "", fmt.Errorf("read body: %w", err)
}
if resp.StatusCode != http.StatusOK {
    return "", fmt.Errorf("provider returned %d: %s", resp.StatusCode, body)
}
```

**Include the body in non-200 errors.** Provider error messages (e.g., "Invalid API key", "File too large") are in the response body. Without it, debugging is painful.

---

## Installation

No new Go module dependencies. The existing `go.mod` requires no changes for any HTTP provider implementation.

For the local provider, document in README/config that the operator must install:
```bash
# System packages (example for Debian/Ubuntu)
apt install ffmpeg

# Build whisper.cpp from source
git clone https://github.com/ggml-org/whisper.cpp
cd whisper.cpp
cmake -B build -DWHISPER_FFMPEG=ON  # enables native OGG/Opus support
cmake --build build --config Release

# Download a model (e.g., base.en for English-only, fast)
bash ./models/download-ggml-model.sh base.en
# Model file: models/ggml-base.en.bin
```

---

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|-------------------------|
| Groq Whisper API (lead default) | OpenAI Whisper API | If already paying for OpenAI API and want one less credential to manage. Groq is faster and cheaper for the same Whisper model architecture. |
| Groq `whisper-large-v3-turbo` | Groq `whisper-large-v3` | If accuracy matters more than speed. Turbo is faster and cheaper; large-v3 is ~15% more accurate on ambiguous speech. |
| Deepgram Nova-3 | Deepgram Nova-2 | Nova-2 if Nova-3 is unavailable in a region or if you need a stable frozen model. Nova-3 is generally recommended as the current best. |
| `os/exec` for local whisper | whisper.cpp Go CGO bindings | NEVER in this project: CGO is disabled. CGO bindings require `libwhisper.a` linkage. |
| Deepgram Nova-3 | Google Cloud STT | Explicitly out of scope (PROJECT.md): "adds heavy SDK dependency, not aligned with minimal deps convention." |
| `ffmpeg` shell-out for format conversion | `ffmpeg-go` (`github.com/u2takey/ffmpeg-go`) | Only if a fluent Go API around ffmpeg is needed. This project has no such need; a single `exec.CommandContext` call with `-ar 16000 -ac 1 -c:a pcm_s16le` flags is clearer and adds zero deps. |

---

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| Deepgram Go SDK (`github.com/deepgram/deepgram-go-sdk`) | Large dependency tree; project convention is no SDKs; the raw API is 15 lines of Go | `net/http` binary body POST |
| OpenAI Go SDK (`github.com/openai/openai-go`) | Same reason as Deepgram SDK | `net/http` multipart POST |
| CGO whisper.cpp bindings (`github.com/ggml-org/whisper.cpp/bindings/go`) | CGO is disabled in this project's builds (`CGO_ENABLED=0`). Linking libwhisper.a would break the build. Also: Go->C callback overhead caused 4x slowdown in benchmarks (62:28 → 14:19 transcription time). | `os/exec` shell-out to whisper-cli binary |
| Static temp file paths (`/tmp/audio.ogg`) for local provider | Race condition when two audio messages are processed concurrently. This is a documented bug pattern in whisper.cpp's own server implementation (issue #2327). | `os.MkdirTemp("", "kapso-whisper-*")` — unique dir per call |
| `http.Post()` or `&http.Client{}` inside `Transcribe()` | Creates a new client per call, throws away TCP connection pooling, risks file descriptor exhaustion under load | Embed `*http.Client` in provider struct, default to `http.DefaultClient` |
| `gpt-4o-transcribe` or `gpt-4o-mini-transcribe` as default model | Response format restricted to `json` only (no `text`, `verbose_json`), pricing is higher, and audio streaming support is in beta | `whisper-1` for OpenAI; `whisper-large-v3-turbo` for Groq |

---

## Stack Patterns by Variant

**If privacy-first / air-gapped deployment:**
- Use `local` provider
- Require `ffmpeg` and `whisper-cli` on host
- Recommended model: `ggml-small.en.bin` for English-only (balance of speed vs accuracy); `ggml-large-v3.bin` for multilingual
- Document that latency will be 5–60 seconds per clip depending on hardware

**If cloud deployment, cost-sensitive:**
- Use `groq` provider
- Model: `whisper-large-v3-turbo`
- At $0.04/hour of audio, a typical WhatsApp user sending 5 voice notes/day at 30s each = $0.10/month

**If cloud deployment, accuracy-sensitive (multilingual):**
- Use `deepgram` provider with `nova-3` model
- Best OGG/Opus native support; explicit documentation of containerized audio handling
- Different auth scheme (`Token` not `Bearer`) — easy to get wrong, worth a dedicated integration test

**If already on OpenAI:**
- Use `openai` provider with `whisper-1`
- Note: CAUTION on `.opus` files. WhatsApp sends OGG-containerized Opus which is supported, but always set filename to `audio.ogg` in the multipart form, not `audio.opus`.

---

## Version Compatibility

| Component | Constraint | Notes |
|-----------|------------|-------|
| Go | 1.22+ (existing) | All stdlib packages used are stable since Go 1.16 |
| OpenAI `whisper-1` model | No versioned endpoint | Stable, unchanged since 2023; OpenAI has not deprecated it |
| Groq `whisper-large-v3-turbo` | No endpoint versioning | Current as of 2026-03-01; check Groq changelog if models change |
| Deepgram `nova-3` | GA (October 2024) | Current as of 2026-03-01; `nova-2` is the stable fallback |
| whisper.cpp | No fixed version requirement | Use latest stable tag; `--output-txt` and `--no-progress` flags have been stable since early versions |
| ffmpeg | 6.x or later | `-c:a pcm_s16le` flag has been stable since ffmpeg 3.x |

---

## Sources

- Groq Whisper API — endpoint, models, OGG support: https://console.groq.com/docs/speech-text (WebSearch summary, MEDIUM confidence; direct page returned 404)
- Groq OpenAI compatibility note: https://console.groq.com/docs/openai (WebFetch, confirmed that transcription uses Groq's own endpoint at `api.groq.com/openai/v1`, MEDIUM confidence)
- Go Groq Whisper example: https://blog.donvitocodes.com/using-groqs-whisper-api-and-go-for-transcribing-audio-to-text (MEDIUM confidence — Go code pattern consistent with Groq docs)
- OpenAI Whisper endpoint and OGG support: https://platform.openai.com/docs/api-reference/audio/createTranscription (WebSearch summary, MEDIUM confidence; direct page returned 403)
- OpenAI `.opus` vs `.ogg` nuance: https://community.openai.com/t/support-for-opus-file-format/1127125 (MEDIUM confidence — community forum, multiple agreeing posts)
- Deepgram pre-recorded endpoint and binary body: https://developers.deepgram.com/docs/pre-recorded-audio (WebFetch successful, MEDIUM confidence — page loaded but format list was abbreviated)
- Deepgram OGG/Opus/WebM format support: https://developers.deepgram.com/docs/supported-audio-formats (WebSearch summary, MEDIUM confidence)
- Deepgram Nova-3 announcement: https://deepgram.com/learn/introducing-nova-3-speech-to-text-api (MEDIUM confidence)
- Deepgram Go raw net/http pattern: https://github.com/orgs/deepgram/discussions/942 (MEDIUM confidence)
- whisper.cpp OGG/Opus + ffmpeg conversion requirement: https://github.com/ggml-org/whisper.cpp (official repo, HIGH confidence via WebSearch)
- whisper.cpp Go exec pattern: https://appliedgo.net/whisper-cli/ (MEDIUM confidence — single source, code consistent with stdlib docs)
- whisper.cpp CGO callback overhead benchmark: https://github.com/ggml-org/whisper.cpp/discussions/312 (MEDIUM confidence — GitHub discussion)
- whisper.cpp temp file collision issue: https://github.com/ggml-org/whisper.cpp/issues/2327 (MEDIUM confidence — official repo issue)
- WhatsApp audio format (OGG/Opus, `audio/ogg; codecs=opus`): https://github.com/chatwoot/chatwoot/issues/12713 (MEDIUM confidence — corroborated by Twilio docs and multiple sources)
- Go multipart form upload pattern: https://pkg.go.dev/mime/multipart (official stdlib, HIGH confidence)
- Go `http.Client` concurrency safety: https://pkg.go.dev/net/http (official stdlib, HIGH confidence)

---
*Stack research for: STT transcription integration in Go WhatsApp-OpenClaw bridge*
*Researched: 2026-03-01*
