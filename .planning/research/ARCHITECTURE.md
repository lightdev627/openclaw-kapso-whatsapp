# Architecture Research

**Domain:** STT integration in a Go WhatsApp-to-AI-gateway bridge
**Researched:** 2026-03-01
**Confidence:** HIGH (codebase directly read; patterns verified against official Go docs and provider APIs)

## Standard Architecture

### System Overview

The new transcription layer intercepts audio messages between the Kapso client
(media download) and the delivery extractor (text production). It adds exactly
one new package (`internal/transcribe/`) and widens two existing call sites.

```
+--------------------------------------------------+
|        Delivery Source (poller / webhook)         |
|  poller.Poller          webhook.Server            |
+--------------------+-----------------------------+
                     |
                     v  []kapso.Message
+--------------------------------------------------+
|      delivery.ExtractText(msg, client, tr)        |
|                                                   |
|  "text"  -> msg.Text.Body               (unchanged)|
|  "audio" -> DownloadMedia + Transcribe  (NEW)     |
|           fallback: "[audio] (mime)"              |
|  other   -> formatMediaMessage          (unchanged)|
+----+---------------------------------------------+
     |          |
     |          | []byte audio + mimeType
     |          v
     |  +-----------------------------------------------+
     |  |          transcribe.Transcriber (interface)    |
     |  |                                               |
     |  |  OpenAIWhisper   GroqWhisper   Deepgram       |
     |  |  (multipart)     (multipart)   (binary body)  |
     |  |                                               |
     |  |  LocalWhisper                                 |
     |  |  (ffmpeg + exec whisper.cpp)                  |
     |  +-----------------------------------------------+
     |
     v  delivery.Event{Text: "[voice] <transcript>"}
+--------------------------------------------------+
|   security.Guard -> gateway.Client -> relay.Relay |
+--------------------------------------------------+
```

All arrows point downward: no circular dependencies are introduced.

### Component Responsibilities

| Component | Responsibility | Communicates With |
|-----------|---------------|-------------------|
| `kapso.Client.DownloadMedia` | Fetches raw audio bytes from Kapso media URL, enforces size limit | Kapso HTTP API |
| `transcribe.Transcriber` (interface) | Single method `Transcribe(ctx, audio []byte, mimeType string) (string, error)` | Called by `delivery.ExtractText` |
| `transcribe.OpenAIWhisper` | Sends multipart/form-data POST to OpenAI `/v1/audio/transcriptions`, configurable model | OpenAI HTTP API |
| `transcribe.GroqWhisper` | Same multipart shape as OpenAI, different base URL, configurable model | Groq HTTP API |
| `transcribe.Deepgram` | Binary-body POST to `https://api.deepgram.com/v1/listen`, model+language as query params | Deepgram HTTP API |
| `transcribe.LocalWhisper` | Writes audio to OS temp file, runs ffmpeg for OGG->WAV if needed, execs whisper.cpp binary, reads stdout, defers cleanup | Local filesystem + two external processes |
| `transcribe.New(cfg)` | Factory: reads `config.TranscribeConfig`, returns correct implementation or nil | `config` package |
| `delivery.ExtractText` | Gain new signature: `ExtractText(msg, client, tr)` where tr is `transcribe.Transcriber` (may be nil) | `kapso.Client`, `transcribe.Transcriber` |
| `config.TranscribeConfig` | New TOML section `[transcribe]`: provider, api_key, model, language, max_audio_size | TOML file + env vars |

## Recommended Project Structure

```
internal/
  transcribe/
    transcribe.go        # Transcriber interface + TranscribeConfig
    openai.go            # OpenAIWhisper (also used for Groq via base URL swap)
    deepgram.go          # Deepgram binary-body implementation
    local.go             # LocalWhisper: ffmpeg + exec whisper.cpp
    new.go               # Factory function New(cfg TranscribeConfig) Transcriber
    openai_test.go       # Table-driven tests, mock HTTP server
    deepgram_test.go     # Table-driven tests, mock HTTP server
    local_test.go        # Table-driven tests, temp dir + mock exec
  kapso/
    client.go            # Existing — add DownloadMedia(ctx, mediaURL, maxBytes) ([]byte, error)
    client_test.go       # Add tests for DownloadMedia
  delivery/
    extract.go           # Modify ExtractText signature to accept Transcriber (may be nil)
    extract_test.go      # Add audio branch tests
  config/
    config.go            # Add TranscribeConfig struct + env overrides
cmd/
  kapso-whatsapp-poller/
    main.go              # Wire transcribe.New(cfg.Transcribe) at startup, pass to ExtractText
```

### Structure Rationale

- **`internal/transcribe/`:** Self-contained package with a clear single concern. Factory pattern means `main.go` never imports provider-specific types directly.
- **`openai.go` covers both OpenAI and Groq:** Their APIs are request-shape identical; the only difference is `BaseURL`. A single struct with a configurable `BaseURL` field avoids duplicated multipart logic. Groq is instantiated by passing `https://api.groq.com/openai/v1` as the base.
- **`kapso.Client.DownloadMedia` on existing client:** Keeps the HTTP client reused (connection pool shared). Audio is downloaded using the same `*http.Client` already configured on `kapso.Client`.
- **nil Transcriber is the zero value for "disabled":** `ExtractText` checks `if tr == nil` before the audio branch. Empty config = nil transcriber = zero behavior change for existing deployments.

## Architectural Patterns

### Pattern 1: Single-Method Interface for Provider Abstraction

**What:** Define the minimal interface at the call site, not in the implementation package.

**When to use:** Always when you have multiple external providers with the same semantic goal. This is idiomatic Go (interface at the consumer, not the implementer).

**Trade-offs:** Simple to test (mock is two lines), easy to add providers, but does not model streaming — acceptable because WhatsApp voice notes are short batch jobs (<2 min), not real-time streams.

**Example:**
```go
// internal/transcribe/transcribe.go
package transcribe

import "context"

// Transcriber converts audio bytes to a text transcript.
// Implementations must be safe for concurrent use by multiple goroutines.
type Transcriber interface {
    Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error)
}
```

### Pattern 2: Shared HTTP Client on Struct (Thread-Safe Reuse)

**What:** Embed `*http.Client` in each provider struct. Accept it as an optional constructor parameter so tests can inject a mock transport. Default to `http.DefaultClient`.

**When to use:** Whenever you make outbound HTTP calls from a long-running service. `http.Client` caches TCP connections; creating one per request throws that away and leaks connections.

**Trade-offs:** Slightly more complex constructor, but critical for connection pool health under load. `http.Client` is documented as safe for concurrent use (HIGH confidence: official Go stdlib source).

**Example:**
```go
// internal/transcribe/openai.go
type OpenAIWhisper struct {
    BaseURL    string       // "https://api.openai.com/v1" or Groq equivalent
    APIKey     string
    Model      string
    Language   string
    HTTPClient *http.Client // defaults to http.DefaultClient if nil
}

func (t *OpenAIWhisper) client() *http.Client {
    if t.HTTPClient != nil {
        return t.HTTPClient
    }
    return http.DefaultClient
}
```

### Pattern 3: Multipart Form for OpenAI/Groq

**What:** Use `mime/multipart` from stdlib to build the request body. No external dependencies.

**When to use:** OpenAI and Groq both require `multipart/form-data` with `file` and `model` fields. The `mime/multipart.Writer` handles boundary generation correctly.

**Trade-offs:** ~20 lines of boilerplate vs. using an SDK. The boilerplate is straightforward and avoids a new dependency (project convention: minimal deps).

**Example:**
```go
func (t *OpenAIWhisper) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
    var buf bytes.Buffer
    w := multipart.NewWriter(&buf)

    // Determine file extension from mimeType for API filename hint.
    ext := extensionFromMIME(mimeType) // e.g. "audio/ogg" -> ".ogg"
    fw, err := w.CreateFormFile("file", "audio"+ext)
    if err != nil {
        return "", fmt.Errorf("create form file: %w", err)
    }
    if _, err := fw.Write(audio); err != nil {
        return "", fmt.Errorf("write audio: %w", err)
    }
    _ = w.WriteField("model", t.Model)
    if t.Language != "" {
        _ = w.WriteField("language", t.Language)
    }
    w.Close()

    req, err := http.NewRequestWithContext(ctx, http.MethodPost,
        t.BaseURL+"/audio/transcriptions", &buf)
    if err != nil {
        return "", fmt.Errorf("new request: %w", err)
    }
    req.Header.Set("Authorization", "Bearer "+t.APIKey)
    req.Header.Set("Content-Type", w.FormDataContentType())

    resp, err := t.client().Do(req)
    // ... read body, decode JSON, return .Text field
}
```

### Pattern 4: Binary Body for Deepgram

**What:** POST the raw audio bytes as the request body. Set `Content-Type` to the audio MIME type. Pass model and language as URL query parameters.

**When to use:** Deepgram's pre-recorded API does not use multipart; it takes binary body with query params. This is simpler than multipart — just `bytes.NewReader(audio)`.

**Trade-offs:** Simpler request construction but different shape from OpenAI pattern, so they cannot share an implementation path.

**Example:**
```go
func (t *Deepgram) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
    u := "https://api.deepgram.com/v1/listen"
    q := url.Values{}
    q.Set("model", t.Model)
    if t.Language != "" {
        q.Set("language", t.Language)
    }
    req, err := http.NewRequestWithContext(ctx, http.MethodPost,
        u+"?"+q.Encode(), bytes.NewReader(audio))
    if err != nil {
        return "", fmt.Errorf("new request: %w", err)
    }
    req.Header.Set("Authorization", "Token "+t.APIKey)
    req.Header.Set("Content-Type", mimeType)
    // ... Do, read, decode results[0].alternatives[0].transcript
}
```

### Pattern 5: Local Whisper via os/exec + Temp Dir

**What:** Write audio to a temp file, optionally convert to 16 kHz mono WAV via ffmpeg, exec the whisper.cpp CLI binary, capture stdout, defer cleanup.

**When to use:** When CGO is disabled (project constraint) and the user wants offline/privacy-first transcription. CGO bindings for whisper.cpp (`github.com/ggml-org/whisper.cpp/bindings/go`) require linking `libwhisper.a`, which violates the CGO disabled constraint.

**Trade-offs:** Two external process dependencies (ffmpeg, whisper.cpp binary). Failure modes are broader. But it is the only viable local path given CGO=disabled.

**Example:**
```go
func (t *LocalWhisper) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
    dir, err := os.MkdirTemp("", "kapso-whisper-*")
    if err != nil {
        return "", fmt.Errorf("mkdirtemp: %w", err)
    }
    defer os.RemoveAll(dir) // always clean up, even on error

    // Write raw audio bytes to temp file.
    rawPath := filepath.Join(dir, "audio"+extensionFromMIME(mimeType))
    if err := os.WriteFile(rawPath, audio, 0o600); err != nil {
        return "", fmt.Errorf("write audio: %w", err)
    }

    // Convert to 16 kHz mono WAV if needed (whisper.cpp requires it).
    inputPath := rawPath
    if !isWAV(mimeType) {
        wavPath := filepath.Join(dir, "audio.wav")
        cmd := exec.CommandContext(ctx, "ffmpeg",
            "-i", rawPath,
            "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le",
            wavPath)
        if out, err := cmd.CombinedOutput(); err != nil {
            return "", fmt.Errorf("ffmpeg: %w: %s", err, out)
        }
        inputPath = wavPath
    }

    // Run whisper.cpp CLI; capture stdout as transcript.
    cmd := exec.CommandContext(ctx, t.BinaryPath,
        "--model", t.ModelPath,
        "--output-txt",  // write .txt alongside input
        "--no-progress",
        inputPath)
    if out, err := cmd.CombinedOutput(); err != nil {
        return "", fmt.Errorf("whisper: %w: %s", err, out)
    }

    // whisper.cpp --output-txt writes <inputPath>.txt
    txtPath := inputPath + ".txt"
    raw, err := os.ReadFile(txtPath)
    if err != nil {
        return "", fmt.Errorf("read transcript: %w", err)
    }
    return strings.TrimSpace(string(raw)), nil
}
```

### Pattern 6: Graceful Fallback in ExtractText

**What:** Wrap the transcription call in an error check. On failure (or nil transcriber), fall back to `[audio] (mimeType)` text representation. Log the error at WARN level. Never block message flow.

**When to use:** Always. Transcription is a best-effort enrichment, not a required gate. The project requirement states: "transcription failure must not break message flow."

**Trade-offs:** Silent fallback hides transcription misconfigurations unless operators check logs. This is the right trade-off for a messaging bridge: a `[audio]` message reaching OpenClaw is better than a dropped message.

**Example:**
```go
// In delivery.ExtractText, audio case:
case "audio":
    if msg.Audio == nil {
        return "", false
    }
    if tr != nil {
        audio, err := client.DownloadMedia(ctx, msg.Audio.ID, maxAudioSize)
        if err == nil {
            if text, err := tr.Transcribe(ctx, audio, msg.Audio.MimeType); err == nil {
                return "[voice] " + text, true
            } else {
                log.Printf("transcription failed for %s: %v", msg.ID, err)
            }
        } else {
            log.Printf("audio download failed for %s: %v", msg.ID, err)
        }
    }
    // Fallback: pass-through label.
    return formatMediaMessage("audio", "", msg.Audio.MimeType, msg.Audio.ID, client), true
```

## Data Flow

### Audio Message Request Flow

```
WhatsApp User sends voice note
    |
    v
Kapso Cloud API (stores audio, delivers message event)
    |
    v
delivery.Source (poller.Poller or webhook.Server)
  produces kapso.Message{Type:"audio", Audio:{ID, MimeType}}
    |
    v
delivery.ExtractText(msg, kapsoClient, transcriber)
    |
    +-- kapsoClient.GetMediaURL(audio.ID)
    |       -> MediaResponse{URL, FileSize}
    |
    +-- kapsoClient.DownloadMedia(ctx, mediaURL, maxBytes)
    |       -> []byte audio (capped at maxAudioSize, e.g. 25MB)
    |
    +-- transcriber.Transcribe(ctx, audio, mimeType)
    |       [HTTP API path]
    |           -> POST multipart or binary body to provider
    |           <- JSON response, extract .text
    |       [local path]
    |           -> os.MkdirTemp
    |           -> os.WriteFile(rawAudio)
    |           -> exec ffmpeg (OGG->WAV, if needed)
    |           -> exec whisper.cpp binary
    |           -> os.ReadFile(.txt output)
    |           -> defer os.RemoveAll(tempDir)
    |
    +-- On success: return "[voice] <transcript>", true
    +-- On error:   log warning, return "[audio] (mimeType) url", true
    |
    v
delivery.Event{Text: "[voice] <transcript>"}
    |
    v
security.Guard.Check(from)
    |
    v
gateway.Client.Send(sessionKey, msgID, taggedText)
    |
    v
OpenClaw AI processes text
```

### Key Data Flows

1. **Audio bytes never leave `delivery.ExtractText` scope:** Downloaded bytes are consumed by the Transcriber and not stored. No disk persistence for HTTP API providers.
2. **Temp files exist only in LocalWhisper.Transcribe scope:** `defer os.RemoveAll(dir)` ensures cleanup on both happy path and all error paths.
3. **nil Transcriber short-circuits at the top of the audio case:** Zero memory allocation when transcription is disabled.
4. **Context propagation:** `ctx` passes from the source goroutine through DownloadMedia and Transcribe, enabling cancellation on daemon shutdown to abort in-flight HTTP calls or subprocess execution.

## Scaling Considerations

| Scale | Architecture Adjustments |
|-------|--------------------------|
| 1 user, low volume | Current approach: synchronous transcription in the event loop goroutine is fine |
| Multiple concurrent users | `http.Client` is already concurrent-safe; each goroutine calling Transcribe is independent; no shared mutable state in provider structs |
| High volume (many simultaneous audio messages) | Move transcription to a bounded worker pool to cap concurrent provider API calls; add timeout per transcription call via `context.WithTimeout` |
| Local whisper at scale | Each `exec.Command` forks a process; becomes a process-count bottleneck; consider whisper.cpp server mode or switch to HTTP provider |

### Scaling Priorities

1. **First bottleneck:** Provider API rate limits. Mitigation: add `context.WithTimeout` (30s) per transcription call at construction time in `New()`. Do not implement retry in v0.2 — let it fall back gracefully.
2. **Second bottleneck:** Local whisper process count. Mitigation: document that local provider is for single-user/low-volume deployments. For multi-user, recommend HTTP API provider.

## Anti-Patterns

### Anti-Pattern 1: Transcription Blocking the Security Guard

**What people do:** Place the transcription call inside the main event loop before the security guard check, or after but still blocking the goroutine with no timeout.

**Why it's wrong:** A slow or hung transcription call blocks that event loop goroutine. If the provider is down, all messages queue behind it. The guard check is cheap and should come first so unauthorized senders are never transcribed (no cost for blocked senders).

**Do this instead:** Keep the current ordering: guard check first, then forward-with-transcription. Add `context.WithTimeout` per transcription to bound worst-case latency.

### Anti-Pattern 2: Creating a New `http.Client` per Transcription Call

**What people do:** `http.Post(url, contentType, body)` or `&http.Client{}` inside `Transcribe()`.

**Why it's wrong:** Throws away TCP connection pooling. Under any load this exhausts file descriptors and increases latency. `http.Client` is documented safe for concurrent use — share one instance.

**Do this instead:** Embed `*http.Client` in the provider struct, defaulting to `http.DefaultClient`. If the caller needs custom timeouts, they pass a pre-configured client at construction time.

### Anti-Pattern 3: Storing Audio Bytes in a Struct Field

**What people do:** Cache the last downloaded audio or accumulate audio bytes across calls in a provider struct field.

**Why it's wrong:** Breaks concurrent safety. Provider structs must be stateless — all per-call data stays on the stack or in local variables inside `Transcribe`.

**Do this instead:** Accept `audio []byte` as a parameter. All state is function-local.

### Anti-Pattern 4: Static Temp File Names for LocalWhisper

**What people do:** `os.WriteFile("/tmp/audio.ogg", ...)` with a hard-coded name.

**Why it's wrong:** Two concurrent transcription calls (possible if multiple audio messages arrive close together) will race on the same file path. This is a documented bug in whisper.cpp's own server implementation (GitHub issue #2327).

**Do this instead:** `os.MkdirTemp("", "kapso-whisper-*")` creates a unique directory per call. All temp files live inside it and are removed together by `defer os.RemoveAll(dir)`.

### Anti-Pattern 5: Ignoring ffmpeg stderr on Failure

**What people do:** `cmd.Run()` without capturing output.

**Why it's wrong:** When ffmpeg fails (missing codec, corrupted audio), the error is opaque: `exit status 1`. The actual reason is in stderr.

**Do this instead:** Use `cmd.CombinedOutput()` and include the captured output in the wrapped error: `fmt.Errorf("ffmpeg: %w: %s", err, capturedOutput)`. This makes failures debuggable from logs.

## Integration Points

### External Services

| Service | Integration Pattern | Notes |
|---------|---------------------|-------|
| OpenAI `/v1/audio/transcriptions` | POST multipart/form-data; fields: `file` (binary), `model`, optional `language`; response JSON `.text` | Returns 200 with `{"text":"..."}`. Auth: `Authorization: Bearer <key>` |
| Groq `/openai/v1/audio/transcriptions` | Identical to OpenAI (same multipart shape); base URL is `https://api.groq.com/openai/v1` | Model names differ: use `whisper-large-v3` for Groq |
| Deepgram `https://api.deepgram.com/v1/listen` | POST binary body; `Content-Type: <audio mime>`; model + language as query params; response JSON `.results.channels[0].alternatives[0].transcript` | Auth: `Authorization: Token <key>` |
| whisper.cpp binary | `exec.CommandContext` with `--model <path> --output-txt --no-progress <wav>`; reads `<wav>.txt` for transcript | Requires 16kHz mono WAV; OGG/Opus must be converted via ffmpeg first |
| ffmpeg binary | `exec.CommandContext` with `-i <input> -ar 16000 -ac 1 -c:a pcm_s16le <output.wav>`; only invoked if input is not already WAV | Optional dependency; only needed for local provider + non-WAV audio |

### Internal Boundaries

| Boundary | Communication | Notes |
|----------|---------------|-------|
| `kapso.Client` <-> `transcribe` | `kapso.Client.DownloadMedia` called by `delivery.ExtractText`; audio bytes passed to Transcriber | No direct coupling between kapso and transcribe packages |
| `delivery` <-> `transcribe` | `delivery.ExtractText` holds a `transcribe.Transcriber` (interface); nil = disabled | Dependency injection, not import-time coupling |
| `config` -> `transcribe.New` | `config.TranscribeConfig` struct passed to factory; factory returns concrete implementation | Config package has no import of transcribe; transcribe imports config types only via the factory signature |
| `main.go` -> all | Wires `transcribe.New(cfg.Transcribe)` at startup, passes result to the event loop; if nil, audio falls through to existing label format | Single wiring point; no change to poller/webhook/security/relay packages |

## Build Order Implications

Dependencies between new components dictate this implementation order:

1. **`config.TranscribeConfig`** — No dependencies. Add struct + env parsing to `config.go`. Required by factory and tests of all other components.

2. **`transcribe.Transcriber` interface + `transcribe.New` factory** — Depends only on config. Defines the contract everything else conforms to.

3. **`kapso.Client.DownloadMedia`** — Depends only on existing `kapso.Client` struct and `kapso.GetMediaURL`. Self-contained addition. Needed by `delivery.ExtractText` but can be tested independently.

4. **HTTP provider implementations (OpenAIWhisper, Deepgram)** — Depend on interface + config. Can be built in parallel with each other. Tested against mock HTTP servers.

5. **`LocalWhisper`** — Depends on interface + config. Depends on `os/exec` and `os.MkdirTemp` patterns. Can be built in parallel with HTTP providers.

6. **`delivery.ExtractText` signature change** — Depends on `transcribe.Transcriber` interface and `kapso.DownloadMedia`. This is the final integration point; widening the signature to accept a Transcriber is a single-line change to the function signature plus a new audio branch.

7. **`main.go` wiring** — Depends on all of the above. One call to `transcribe.New(cfg.Transcribe)`, pass result into `ExtractText`.

## Sources

- Go `http.Client` concurrency safety: https://go.dev/src/net/http/client.go (official stdlib, HIGH confidence)
- Go `os.MkdirTemp` + `defer os.RemoveAll` pattern: https://pkg.go.dev/os (official, HIGH confidence)
- Go `context.Context` for goroutine cancellation: https://go.dev/blog/context (official Go blog, HIGH confidence)
- OpenAI Whisper API shape (multipart, `file`+`model` fields, `.text` response): https://github.com/openai/openai-go (official OpenAI Go library, HIGH confidence)
- Groq API compatible with OpenAI: https://console.groq.com/docs/openai (MEDIUM confidence — verified by multiple community sources)
- Deepgram pre-recorded binary body + query params: https://developers.deepgram.com/docs/pre-recorded-audio (official Deepgram docs, HIGH confidence)
- whisper.cpp CLI `--output-txt` flag, 16kHz WAV requirement: https://github.com/ggml-org/whisper.cpp (official repo, HIGH confidence)
- ffmpeg OGG/Opus -> WAV flags for whisper: https://github.com/ggml-org/whisper.cpp (whisper.cpp docs, HIGH confidence)
- Temp file collision bug pattern (avoid static names): https://github.com/ggml-org/whisper.cpp/issues/2327 (official repo issue, MEDIUM confidence)
- Applied Go whisper.cpp exec pattern: https://appliedgo.net/whisper-cli/ (MEDIUM confidence — single source, pattern consistent with Go stdlib docs)

---
*Architecture research for: Go STT integration in WhatsApp-OpenClaw bridge*
*Researched: 2026-03-01*
