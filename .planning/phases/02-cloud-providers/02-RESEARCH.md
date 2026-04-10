# Phase 2: Cloud Providers - Research

**Researched:** 2026-03-01
**Domain:** HTTP-based audio transcription provider implementations in Go (OpenAI Whisper, Groq Whisper, Deepgram Nova) using stdlib only
**Confidence:** HIGH (existing project research files verified; API shapes confirmed via official docs and multiple community sources)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

#### Provider API behavior
- OpenAI and Groq share one struct with configurable `BaseURL` — no duplicated HTTP logic
- Auto-detect model defaults per provider: openai defaults to `whisper-1`, groq defaults to `whisper-large-v3`. User can override via config
- MIME-derived filename in multipart form: `audio/ogg` → `audio.ogg`, `audio/mpeg` → `audio.mp3`
- Omit language parameter entirely when config field is empty (let API auto-detect)
- Request `verbose_json` response format — parse transcript from response JSON. Enables future `no_speech_prob` quality guard (Phase 4) and debug logging without changing provider code later

#### Error & retry strategy
- Retry as specified in requirements: 3 attempts, 1s base, 2x factor, random jitter up to 25%. Total max ~7s wait
- Retry logic lives as an interface wrapper (`retryTranscriber` wrapping any `Transcriber`), not inside each provider. Clean separation, independently testable
- Error on exhaustion: last error with attempt count — "transcribe failed after 3 attempts: 503 Service Unavailable"
- Per-transcription timeout (`context.WithTimeout` using `cfg.Timeout`) enforced inside the retry wrapper in this phase

#### MIME handling
- Strip codec params, map known variants: `audio/ogg; codecs=opus` → `audio/ogg`, `audio/opus` → `audio/ogg`
- Unsupported MIME types: try anyway, pass normalized MIME to provider and let it decide. Log a warning. Only fail if provider rejects it
- MIME normalization function lives in `internal/transcribe/mime.go` — co-located with providers
- Multipart Content-Type header uses the normalized MIME type

#### Deepgram differences
- Deepgram gets its own separate struct implementing `Transcriber` — different enough (binary body, query params, different response shape) that sharing would be forced
- Missing/empty channels in response: return error. Treat as failed transcription. Strict and predictable
- `smart_format=true` always on — better punctuation for chat messages with no downside
- One `api_key` config field for all providers — each provider formats its own auth header internally (OpenAI/Groq use `Bearer`, Deepgram uses `Token`)

### Claude's Discretion
- Exact multipart boundary construction details
- HTTP client configuration (timeouts, transport settings)
- Internal error type design
- Test helper organization

### Deferred Ideas (OUT OF SCOPE)

None — discussion stayed within phase scope
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| PROV-01 | OpenAI Whisper provider — `POST /v1/audio/transcriptions`, multipart form (file, model, language), configurable model (default `whisper-1`) | OpenAI endpoint verified; multipart fields documented; verbose_json response structure confirmed |
| PROV-02 | Groq Whisper provider — same multipart shape as OpenAI with different base URL (`api.groq.com/openai/v1`), configurable model (default `whisper-large-v3`) | Groq endpoint and OpenAI-compatible shape confirmed; base URL documented |
| PROV-03 | Deepgram Nova provider — `POST /v1/listen`, binary body with Content-Type set to audio MIME, query params (model, smart_format, language), configurable model (default `nova-3`) | Deepgram binary-body pattern, query params, and response structure confirmed |
| PROV-04 | OpenAI and Groq share implementation via configurable `BaseURL` field — no duplicated code | Single struct pattern with configurable BaseURL documented with code examples |
| INFR-03 | OGG/Opus MIME normalization — use `mime/multipart.CreatePart` (not `CreateFormFile`) for correct Content-Type | CreatePart vs CreateFormFile difference confirmed; normalization variants documented; mime.go placement decided |
| TEST-01 | Table-driven tests for each cloud provider with HTTP test server mocking API responses | httptest.NewServer pattern from kapso/client_test.go is established pattern; verbose_json fixture structure documented |
| TEST-05 | Retry logic test (429, 5xx, success after retry, exhausted retries) | Retry wrapper pattern and status code handling documented; retryTranscriber interface wrapper approach confirmed |
</phase_requirements>

---

## Summary

Phase 2 implements three cloud transcription providers — OpenAI Whisper, Groq Whisper, and Deepgram Nova — as concrete implementations of the `transcribe.Transcriber` interface already in place from Phase 1. The project has mature prior research (`.planning/research/`) that covers provider API shapes, Go patterns, and pitfalls in detail. This phase research refines that work specifically for the Phase 2 scope: HTTP cloud providers, MIME normalization, and retry infrastructure.

The implementation is pure Go stdlib — zero new module dependencies. OpenAI and Groq share one `openAIWhisper` struct with a configurable `BaseURL`; multipart construction uses `mime/multipart.CreatePart` (not `CreateFormFile`) to set the correct `Content-Type` per INFR-03. Deepgram uses a binary body POST with query parameters and a separate struct. The retry wrapper is a `retryTranscriber` struct that wraps any `Transcriber`, keeping retry logic cleanly decoupled from provider logic and independently testable.

The most subtle technical requirement is the interaction between MIME normalization and multipart construction. WhatsApp delivers audio as `audio/ogg; codecs=opus`. This must be normalized to `audio/ogg` before the multipart part header is set. The normalized MIME maps to a filename (`audio.ogg`) used in the `Content-Disposition` header. This normalization logic lives in `internal/transcribe/mime.go` and is used by both the OpenAI/Groq shared struct and the Deepgram struct.

**Primary recommendation:** Implement in this order: (1) `mime.go` normalizer, (2) `openai.go` shared struct for OpenAI + Groq, (3) `deepgram.go`, (4) `retry.go` wrapper. Replace `New()` stubs last once each provider has passing tests.

---

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `net/http` (stdlib) | Go 1.22 | All HTTP calls to STT providers | Project convention: no SDKs. `http.Client` is concurrent-safe, connection-pooling, already used in `kapso.Client`. |
| `mime/multipart` (stdlib) | Go 1.22 | Build `multipart/form-data` bodies for OpenAI/Groq | OpenAI and Groq require multipart. `Writer.CreatePart` gives full control over `Content-Type` per part. Zero new deps. |
| `net/url` (stdlib) | Go 1.22 | Build query parameter strings for Deepgram | `url.Values.Encode()` handles escaping. Used for `model`, `language`, `smart_format` params. |
| `encoding/json` (stdlib) | Go 1.22 | Decode JSON responses from all three providers | Already used throughout the codebase. |
| `bytes` (stdlib) | Go 1.22 | Buffer multipart body and binary audio body | `bytes.Buffer` for multipart, `bytes.NewReader` for Deepgram binary body. |
| `math/rand` (stdlib) | Go 1.22 | Jitter computation in retry wrapper | `rand.Int63n` sufficient; no need for crypto/rand. |
| `time` (stdlib) | Go 1.22 | Retry backoff sleep durations | `time.Sleep` with exponential backoff + jitter |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `net/http/httptest` (stdlib) | Go 1.22 | Mock HTTP servers in tests | All provider tests: `httptest.NewServer` receives multipart/binary requests, returns fixture JSON |
| `strings` (stdlib) | Go 1.22 | MIME type stripping (`strings.Cut`, `strings.Split`) | Normalize `audio/ogg; codecs=opus` → `audio/ogg` |
| `net/textproto` (stdlib) | Go 1.22 | Build `MIMEHeader` for `multipart.Writer.CreatePart` | Required for INFR-03: set correct `Content-Type` on multipart file part |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `net/http` + multipart | OpenAI Go SDK (`github.com/openai/openai-go`) | SDK adds a large dependency for a 20-line request; project convention is no SDKs |
| `net/http` binary body | Deepgram Go SDK | SDK has heavy dependency tree; raw API is 15 lines |
| stdlib retry (hand-rolled) | `github.com/cenkalti/backoff/v4` | External dep; project has minimal deps convention; the retry spec is simple enough for 25 lines of stdlib |

**Installation:** No new Go modules required. `go.mod` unchanged.

---

## Architecture Patterns

### Recommended Project Structure

```
internal/transcribe/
├── transcribe.go        # Transcriber interface (already exists from Phase 1)
├── mime.go              # NEW: NormalizeMIME() + mimeToFilename() helpers
├── openai.go            # NEW: openAIWhisper struct (shared for OpenAI + Groq)
├── deepgram.go          # NEW: deepgramProvider struct
├── retry.go             # NEW: retryTranscriber wrapper struct
├── openai_test.go       # NEW: table-driven tests + httptest mock server
├── deepgram_test.go     # NEW: table-driven tests + httptest mock server
└── retry_test.go        # NEW: retry behavior tests (429, 5xx, success-after-retry)
```

The `transcribe.go` and factory `New()` function are already in place from Phase 1. The `New()` stubs for `openai`, `groq`, `deepgram` return `fmt.Errorf("not yet implemented")` — Phase 2 replaces these stubs with real provider construction wrapped in the retry wrapper.

### Pattern 1: Shared OpenAI/Groq Struct with Configurable BaseURL

**What:** A single `openAIWhisper` struct implements `Transcriber` for both OpenAI and Groq. The `BaseURL` field is the only difference between the two.

**When to use:** Any time two providers share an identical wire format (multipart fields, response shape, auth header). Violates DRY if duplicated.

**Example:**
```go
// Source: internal/transcribe/openai.go
package transcribe

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "mime/multipart"
    "net/http"
    "net/textproto"
)

type openAIWhisper struct {
    BaseURL    string       // "https://api.openai.com/v1" or "https://api.groq.com/openai/v1"
    APIKey     string
    Model      string       // "whisper-1" or "whisper-large-v3"
    Language   string       // omit when empty — let API auto-detect
    HTTPClient *http.Client // nil → http.DefaultClient
}

func (t *openAIWhisper) client() *http.Client {
    if t.HTTPClient != nil {
        return t.HTTPClient
    }
    return http.DefaultClient
}

func (t *openAIWhisper) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
    norm := NormalizeMIME(mimeType)         // "audio/ogg; codecs=opus" → "audio/ogg"
    filename := mimeToFilename(norm)        // "audio/ogg" → "audio.ogg"

    var buf bytes.Buffer
    w := multipart.NewWriter(&buf)

    // Use CreatePart (not CreateFormFile) to set correct Content-Type per INFR-03.
    h := make(textproto.MIMEHeader)
    h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
    h.Set("Content-Type", norm)
    fw, err := w.CreatePart(h)
    if err != nil {
        return "", fmt.Errorf("create form part: %w", err)
    }
    if _, err := fw.Write(audio); err != nil {
        return "", fmt.Errorf("write audio: %w", err)
    }
    _ = w.WriteField("model", t.Model)
    _ = w.WriteField("response_format", "verbose_json")
    if t.Language != "" {
        _ = w.WriteField("language", t.Language)
    }
    w.Close() // write trailing boundary — mandatory

    req, err := http.NewRequestWithContext(ctx, http.MethodPost,
        t.BaseURL+"/audio/transcriptions", &buf)
    if err != nil {
        return "", fmt.Errorf("new request: %w", err)
    }
    req.Header.Set("Authorization", "Bearer "+t.APIKey)
    req.Header.Set("Content-Type", w.FormDataContentType()) // includes boundary

    resp, err := t.client().Do(req)
    if err != nil {
        return "", fmt.Errorf("http do: %w", err)
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return "", fmt.Errorf("read response: %w", err)
    }
    if resp.StatusCode != http.StatusOK {
        return "", fmt.Errorf("provider returned %d: %s", resp.StatusCode, body)
    }

    var result struct {
        Text string `json:"text"`
    }
    if err := json.Unmarshal(body, &result); err != nil {
        return "", fmt.Errorf("decode response: %w", err)
    }
    return result.Text, nil
}
```

**Factory construction for OpenAI and Groq:**
```go
// In New(), replacing the "not yet implemented" stubs:
case "openai":
    model := cfg.Model
    if model == "" {
        model = "whisper-1"
    }
    p = &openAIWhisper{
        BaseURL:  "https://api.openai.com/v1",
        APIKey:   cfg.APIKey,
        Model:    model,
        Language: cfg.Language,
    }

case "groq":
    model := cfg.Model
    if model == "" {
        model = "whisper-large-v3"
    }
    p = &openAIWhisper{
        BaseURL:  "https://api.groq.com/openai/v1",
        APIKey:   cfg.APIKey,
        Model:    model,
        Language: cfg.Language,
    }
```

### Pattern 2: Deepgram Binary Body with Query Params

**What:** POST raw audio bytes as the request body. Set `Content-Type` to the normalized MIME type. Pass `model`, `language`, and `smart_format` as query parameters. Auth uses `Token` prefix (not `Bearer`).

**When to use:** Deepgram's pre-recorded API. Not multipart — simpler request construction.

**Example:**
```go
// Source: internal/transcribe/deepgram.go
package transcribe

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
)

type deepgramProvider struct {
    APIKey     string
    Model      string       // default "nova-3"
    Language   string       // omit when empty
    HTTPClient *http.Client
}

func (t *deepgramProvider) client() *http.Client {
    if t.HTTPClient != nil {
        return t.HTTPClient
    }
    return http.DefaultClient
}

func (t *deepgramProvider) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
    norm := NormalizeMIME(mimeType)

    q := url.Values{}
    q.Set("model", t.Model)
    q.Set("smart_format", "true") // always on per CONTEXT.md decision
    if t.Language != "" {
        q.Set("language", t.Language)
    }

    req, err := http.NewRequestWithContext(ctx, http.MethodPost,
        "https://api.deepgram.com/v1/listen?"+q.Encode(),
        bytes.NewReader(audio))
    if err != nil {
        return "", fmt.Errorf("new request: %w", err)
    }
    req.Header.Set("Authorization", "Token "+t.APIKey) // "Token", not "Bearer"
    req.Header.Set("Content-Type", norm)

    resp, err := t.client().Do(req)
    if err != nil {
        return "", fmt.Errorf("http do: %w", err)
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return "", fmt.Errorf("read response: %w", err)
    }
    if resp.StatusCode != http.StatusOK {
        return "", fmt.Errorf("deepgram returned %d: %s", resp.StatusCode, body)
    }

    var result struct {
        Results struct {
            Channels []struct {
                Alternatives []struct {
                    Transcript string `json:"transcript"`
                } `json:"alternatives"`
            } `json:"channels"`
        } `json:"results"`
    }
    if err := json.Unmarshal(body, &result); err != nil {
        return "", fmt.Errorf("decode response: %w", err)
    }
    if len(result.Results.Channels) == 0 || len(result.Results.Channels[0].Alternatives) == 0 {
        return "", fmt.Errorf("deepgram response missing channels/alternatives")
    }
    return result.Results.Channels[0].Alternatives[0].Transcript, nil
}
```

### Pattern 3: retryTranscriber Interface Wrapper

**What:** A `retryTranscriber` struct wraps any `Transcriber`. It intercepts the `Transcribe` call, retries on 429/5xx errors up to 3 attempts with exponential backoff + 25% jitter, and returns the last error with attempt count on exhaustion.

**When to use:** Wrap any cloud provider inside `New()` before returning it. This keeps retry logic out of provider implementations and makes it independently testable.

**Retry spec (from CONTEXT.md):** 3 attempts, 1s base, 2x factor, jitter up to 25% of current delay. Max wait before attempt 3 is approximately 1s + 2s + jitter = ~3s cumulative. Total max ~7s.

**Why the wrapper pattern:** Retry logic is pure orchestration — it should not know about multipart construction, auth headers, or response parsing. Wrapping the interface keeps each concern in its own type.

**Example:**
```go
// Source: internal/transcribe/retry.go
package transcribe

import (
    "context"
    "fmt"
    "math/rand"
    "net/http"
    "time"
)

type retryTranscriber struct {
    inner    Transcriber
    attempts int           // 3
    base     time.Duration // 1s
    factor   float64       // 2.0
    jitter   float64       // 0.25 (25%)
    timeout  time.Duration // from cfg.Timeout
    // nowFunc for testability (injectable)
    sleepFunc func(time.Duration) // defaults to time.Sleep
}

func newRetryTranscriber(inner Transcriber, timeout time.Duration) *retryTranscriber {
    return &retryTranscriber{
        inner:     inner,
        attempts:  3,
        base:      1 * time.Second,
        factor:    2.0,
        jitter:    0.25,
        timeout:   timeout,
        sleepFunc: time.Sleep,
    }
}

// isRetryable returns true for 429 and 5xx status codes.
// The error from provider.Transcribe includes the status code as "provider returned NNN: ..."
// Parse it, or use a sentinel error type.
func isRetryable(err error) bool {
    // Simple string scan approach (no custom error types needed):
    // Provider errors are fmt.Errorf("provider returned %d: %s", code, body)
    // Alternative: define a ProviderError{StatusCode int} type.
    // Either approach works; custom type is cleaner for tests.
    return false // placeholder — implementation decides the approach
}

func (r *retryTranscriber) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
    // Wrap with per-transcription timeout.
    if r.timeout > 0 {
        var cancel context.CancelFunc
        ctx, cancel = context.WithTimeout(ctx, r.timeout)
        defer cancel()
    }

    delay := r.base
    var lastErr error
    for attempt := 1; attempt <= r.attempts; attempt++ {
        text, err := r.inner.Transcribe(ctx, audio, mimeType)
        if err == nil {
            return text, nil
        }
        lastErr = err
        if attempt == r.attempts || !isRetryable(err) {
            break
        }
        // Apply jitter: delay + random(0, delay * jitter)
        jitterNs := rand.Int63n(int64(float64(delay) * r.jitter))
        r.sleepFunc(delay + time.Duration(jitterNs))
        delay = time.Duration(float64(delay) * r.factor)
    }
    return "", fmt.Errorf("transcribe failed after %d attempts: %w", r.attempts, lastErr)
}
```

**Factory integration:**
```go
// In New(), after constructing provider p:
tr := newRetryTranscriber(p, time.Duration(cfg.Timeout)*time.Second)
return tr, nil
```

### Pattern 4: MIME Normalization Helper

**What:** A single `NormalizeMIME(mimeType string) string` function strips codec parameters and maps known OGG/Opus variants to `audio/ogg`. Lives in `mime.go` co-located with providers.

**When to use:** Called by all providers before using `mimeType` in headers or filenames.

**Example:**
```go
// Source: internal/transcribe/mime.go
package transcribe

import "strings"

// NormalizeMIME normalises WhatsApp audio MIME variants for STT provider compatibility.
//
//   "audio/ogg; codecs=opus" → "audio/ogg"
//   "audio/opus"             → "audio/ogg"   (Opus in OGG container — WhatsApp format)
//   "audio/ogg"              → "audio/ogg"   (no-op)
//   anything else            → base type with params stripped, passed through
func NormalizeMIME(mimeType string) string {
    // Strip parameters (everything after ';')
    base, _, _ := strings.Cut(mimeType, ";")
    base = strings.TrimSpace(strings.ToLower(base))

    switch base {
    case "audio/opus":
        return "audio/ogg"
    default:
        return base
    }
}

// mimeToFilename maps a normalized MIME type to a filename for multipart Content-Disposition.
func mimeToFilename(mimeType string) string {
    switch mimeType {
    case "audio/ogg":
        return "audio.ogg"
    case "audio/mpeg":
        return "audio.mp3"
    case "audio/mp4":
        return "audio.mp4"
    case "audio/wav", "audio/x-wav":
        return "audio.wav"
    case "audio/webm":
        return "audio.webm"
    case "audio/flac":
        return "audio.flac"
    default:
        return "audio.bin"
    }
}
```

### Pattern 5: Retryable Error Detection

**What:** Provider errors include an HTTP status code. The retry wrapper needs to classify errors as retryable (429, 5xx) vs. permanent (4xx except 429).

**Recommendation:** Define a `httpError` type so the retry wrapper can type-assert without parsing error strings.

**Example:**
```go
// In a shared location (e.g., transcribe.go or retry.go):

// httpError is returned by providers when the HTTP status is non-200.
type httpError struct {
    StatusCode int
    Body       string
}

func (e *httpError) Error() string {
    return fmt.Sprintf("provider returned %d: %s", e.StatusCode, e.Body)
}

// isRetryable checks if an error warrants a retry.
func isRetryable(err error) bool {
    var herr *httpError
    if errors.As(err, &herr) {
        return herr.StatusCode == http.StatusTooManyRequests || herr.StatusCode >= 500
    }
    return false
}
```

### Anti-Patterns to Avoid

- **Using `CreateFormFile` for multipart audio part:** Hardcodes `Content-Type: application/octet-stream`. Use `CreatePart` with explicit `textproto.MIMEHeader` per INFR-03.
- **Using "Bearer" auth for Deepgram:** Deepgram uses `Authorization: Token <key>`, not `Authorization: Bearer <key>`. Using Bearer results in a 401.
- **Forgetting `w.Close()` after multipart writing:** The trailing boundary is written by `w.Close()`. Omitting it produces a malformed request body that the API rejects.
- **Creating a new `*http.Client` inside `Transcribe()`:** Throws away TCP connection pooling, risks fd exhaustion. Embed the client in the provider struct.
- **Retry on 4xx errors:** Only 429 and 5xx are retryable. Retrying on 400 (bad request) or 401 (auth) is pointless and wastes quota.
- **Parsing `verbose_json` text field from the top-level:** `verbose_json` response has `text` at the top level — same as `json` format. No need to walk segments to assemble transcript.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Multipart boundary generation | Custom boundary string | `multipart.NewWriter(&buf)` + `w.FormDataContentType()` | Boundaries must be unique per request and not appear in the body — stdlib handles this |
| Query parameter URL encoding | Manual string concatenation | `url.Values{}` + `.Encode()` | Handles URL encoding of special chars in model names or language codes |
| Exponential backoff sleep | `time.Sleep(1s); time.Sleep(2s);` etc. | The retry wrapper pattern shown above | Jitter prevents thundering herd; formula must be correct |
| JSON response decoding | Manual byte scanning | `encoding/json.Unmarshal` | Provider responses are valid JSON; stdlib handles all edge cases |

**Key insight:** Everything in this phase is achievable with stdlib packages that are already imported elsewhere in the codebase (`encoding/json`, `net/http`, `bytes`). Adding external dependencies for a retry wrapper or multipart helper would violate the minimal-deps project convention without any benefit.

---

## Common Pitfalls

### Pitfall 1: `CreateFormFile` vs `CreatePart` (INFR-03)
**What goes wrong:** `multipart.Writer.CreateFormFile` hardcodes `Content-Type: application/octet-stream` on the file part. Some providers use this for format detection; sending the wrong MIME type can cause silent acceptance of garbled transcription or outright rejection.
**Why it happens:** `CreateFormFile` looks like the right API — it handles `Content-Disposition` automatically — but the Content-Type is not customizable.
**How to avoid:** Always use `writer.CreatePart(textproto.MIMEHeader)` with explicit `Content-Disposition` and `Content-Type` headers. The extra 5 lines are worth the control.
**Warning signs:** API returns 400 "unsupported format" for OGG files that work fine as WAV.

### Pitfall 2: Deepgram Auth Header Uses "Token" not "Bearer"
**What goes wrong:** Setting `Authorization: Bearer <key>` returns HTTP 401 from Deepgram. OpenAI and Groq use `Bearer`; Deepgram uses `Token`. The test against a mock server passes because the mock doesn't validate auth; the real API rejects it.
**Why it happens:** Copy-pasting from the OpenAI/Groq implementation.
**How to avoid:** Each provider builds its own auth header internally. Code review: grep for `Bearer` in `deepgram.go` before merging.
**Warning signs:** HTTP 401 from Deepgram in integration testing with a real API key.

### Pitfall 3: Forgetting to Call `w.Close()` on multipart.Writer
**What goes wrong:** The multipart body is missing its trailing `--boundary--` terminator. The server receives a truncated body and returns 400.
**Why it happens:** `Close()` on a writer is unusual Go style (most writers don't need Close). Easy to forget in the happy path, especially when errors cause early returns before `defer w.Close()` can be placed.
**How to avoid:** Place `defer w.Close()` immediately after `w := multipart.NewWriter(&buf)` — but be aware `Close()` writes to the buffer, so the request must be constructed _after_ `w.Close()`. Use explicit `w.Close()` before creating the request, not defer.
**Warning signs:** HTTP 400 "invalid multipart body" or truncated request errors.

### Pitfall 4: Deepgram Empty Channels Response
**What goes wrong:** Deepgram returns HTTP 200 with a valid JSON body but `results.channels` is empty (can happen with silent/zero-length audio or certain model errors). A naive implementation returns `""` as a successful transcript.
**Why it happens:** Developers check only `resp.StatusCode != 200` and assume non-empty channels.
**How to avoid:** Explicitly check `len(channels) == 0 || len(channels[0].Alternatives) == 0` and return an error. Per CONTEXT.md: "Missing/empty channels in response: return error."
**Warning signs:** Empty transcript strings appearing in message log as `[voice] ` (with trailing space but no text).

### Pitfall 5: retryTranscriber Not Wrapping the Timeout Correctly
**What goes wrong:** The `context.WithTimeout` is applied once before the retry loop. After the timeout expires on the first attempt, all subsequent attempts immediately fail with `context.DeadlineExceeded` — making the retry useless and masking the original provider error.
**Why it happens:** Applying the timeout to the parent context instead of using a fresh child context per attempt (or applying the overall timeout to the full retry span, not each attempt).
**How to avoid:** Apply `cfg.Timeout` as the deadline for the entire retry span (all 3 attempts combined), not per attempt. This is consistent with CONTEXT.md: "Per-transcription timeout (`context.WithTimeout` using `cfg.Timeout`) enforced inside the retry wrapper."
**Warning signs:** All retries fail instantly with `context.DeadlineExceeded` rather than the expected provider error.

### Pitfall 6: verbose_json Response Parsing
**What goes wrong:** The `verbose_json` format returns a `text` field at the top level AND segments with per-segment `text` fields. Code that walks segments to build the transcript will work but produces identical output to `result.Text` — unnecessarily complex.
**Why it happens:** Assuming `verbose_json` requires different parsing than `json` format.
**How to avoid:** Parse `result.Text` from the top-level — it is the full concatenated transcript regardless of format. The `segments` array with `avg_logprob` and `no_speech_prob` is available for Phase 4 quality guard use.
**Warning signs:** Tests passing but transcript assembly code is more complex than a single JSON field read.

---

## Code Examples

Verified patterns from official sources and prior project research:

### OpenAI/Groq: verbose_json Response Shape
```go
// Source: OpenAI API docs + community verification (MEDIUM confidence)
// verbose_json top-level structure for whisper-1:
var result struct {
    Task     string  `json:"task"`
    Language string  `json:"language"`
    Duration float64 `json:"duration"`
    Text     string  `json:"text"`   // ← full transcript here
    Segments []struct {
        ID           int     `json:"id"`
        Seek         int     `json:"seek"`
        Start        float64 `json:"start"`
        End          float64 `json:"end"`
        Text         string  `json:"text"`
        AvgLogprob   float64 `json:"avg_logprob"`   // Phase 4: INFR-04
        NoSpeechProb float64 `json:"no_speech_prob"` // Phase 4: TRNS-04
    } `json:"segments"`
}
// Extract transcript: result.Text
// Phase 4 will add: log result.Segments[i].AvgLogprob, NoSpeechProb
```

### httptest Mock Server Pattern (from kapso/client_test.go)
```go
// Source: internal/kapso/client_test.go (existing codebase pattern)
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // Verify auth header
    if r.Header.Get("Authorization") != "Bearer test-key" {
        w.WriteHeader(http.StatusUnauthorized)
        return
    }
    // Return fixture response
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    fmt.Fprint(w, `{"text":"hello world","language":"english","duration":2.5}`)
}))
defer srv.Close()

provider := &openAIWhisper{
    BaseURL:    srv.URL,
    APIKey:     "test-key",
    Model:      "whisper-1",
    HTTPClient: srv.Client(),
}
```

### Deepgram Mock Server (verify binary body + query params)
```go
// Source: established httptest pattern, adapted for Deepgram
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // Verify Auth header uses "Token" not "Bearer"
    if r.Header.Get("Authorization") != "Token test-dg-key" {
        w.WriteHeader(http.StatusUnauthorized)
        return
    }
    // Verify query params
    if r.URL.Query().Get("smart_format") != "true" {
        w.WriteHeader(http.StatusBadRequest)
        return
    }
    // Verify Content-Type is the audio MIME (binary body)
    if !strings.HasPrefix(r.Header.Get("Content-Type"), "audio/") {
        w.WriteHeader(http.StatusBadRequest)
        return
    }
    // Return Deepgram response fixture
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    fmt.Fprint(w, `{"results":{"channels":[{"alternatives":[{"transcript":"hello from deepgram"}]}]}}`)
}))
defer srv.Close()
```

### Retry Test Pattern
```go
// Source: established Go test pattern for stateful HTTP servers
callCount := 0
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    callCount++
    if callCount < 3 {
        w.WriteHeader(http.StatusTooManyRequests) // 429 on first two attempts
        return
    }
    w.WriteHeader(http.StatusOK)
    fmt.Fprint(w, `{"text":"success after retry"}`)
}))
defer srv.Close()

// Use a retryTranscriber with a mock sleepFunc to avoid real delays in tests:
rt := &retryTranscriber{
    inner:     &openAIWhisper{BaseURL: srv.URL, ...},
    attempts:  3,
    base:      1 * time.Millisecond, // fast for tests
    factor:    2.0,
    jitter:    0.0,
    sleepFunc: func(d time.Duration) {}, // no-op sleep
}
```

### MIME Normalization Table Test
```go
// Source: pattern derived from requirements; verified against CONTEXT.md decisions
tests := []struct {
    input string
    want  string
}{
    {"audio/ogg; codecs=opus", "audio/ogg"},
    {"audio/ogg",             "audio/ogg"},
    {"audio/opus",            "audio/ogg"},
    {"audio/mpeg",            "audio/mpeg"},
    {"audio/mp4",             "audio/mp4"},
    {"audio/webm; codecs=vp9","audio/webm"},
    {"",                      ""},
}
for _, tc := range tests {
    got := NormalizeMIME(tc.input)
    if got != tc.want {
        t.Errorf("NormalizeMIME(%q) = %q, want %q", tc.input, got, tc.want)
    }
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| OpenAI `whisper-1` only | `whisper-1` + `gpt-4o-transcribe` family | 2024-2025 | `gpt-4o-*` models only support `json` format (not `verbose_json`); use `whisper-1` for `verbose_json` support |
| Groq `whisper-large-v3` | `whisper-large-v3-turbo` available | Late 2024 | Turbo is faster/cheaper at slight accuracy cost; default remains `whisper-large-v3` per CONTEXT.md |
| Deepgram `nova-2` | `nova-3` (GA October 2024) | October 2024 | Nova-3 has 54% WER reduction; use as default (`nova-3`) |
| `CreateFormFile` for multipart | `CreatePart` with explicit MIMEHeader | N/A (always existed) | Correct Content-Type on file part |

**Deprecated/outdated:**
- Groq `distil-whisper-large-v3-en`: English-only, lower accuracy, superseded by turbo. Do not use as default.
- OpenAI `gpt-4o-transcribe` as default: JSON-only response, no `verbose_json`, breaks Phase 4 quality guard. Keep `whisper-1` as default.

---

## Open Questions

1. **Exact Groq rate limits under audio load**
   - What we know: Groq has per-minute limits on audio seconds transcribed; exact numbers vary by tier
   - What's unclear: Free tier limit vs. paid tier limit for a single bridge deployment
   - Recommendation: 429 is already in retry scope; this is handled. Document in config that operators should check `console.groq.com/settings/limits` if they see frequent 429s.

2. **Deepgram response nesting for `smart_format=true` with edge case audio**
   - What we know: `results.channels[0].alternatives[0].transcript` is the documented path; empty channels return error per CONTEXT.md decision
   - What's unclear: Whether `smart_format=true` ever produces a different response structure
   - Recommendation: Test with a real Deepgram fixture (obtained from their playground or docs) to validate the JSON shape. The mock server in tests should mirror the real fixture.

3. **`verbose_json` support on Groq**
   - What we know: Groq docs list `verbose_json` as a supported `response_format` option
   - What's unclear: Whether all `verbose_json` fields (segments, avg_logprob, no_speech_prob) are returned by Groq or if it is OpenAI-only
   - Recommendation: Request `verbose_json` as per CONTEXT.md decision; if Groq omits segment fields, that is acceptable since Phase 2 only reads `text`. Phase 4 will deal with segment fields.

---

## Sources

### Primary (HIGH confidence)
- `pkg.go.dev/mime/multipart` — CreatePart, CreateFormFile, Writer.FormDataContentType, Writer.WriteField
- `pkg.go.dev/net/http` — http.Client, http.NewRequestWithContext
- `pkg.go.dev/net/http/httptest` — NewServer pattern
- Internal codebase: `internal/kapso/client_test.go` — established httptest.NewServer + rewriteTransport pattern
- Internal codebase: `internal/transcribe/transcribe.go` — Transcriber interface, existing New() factory
- Internal codebase: `.planning/research/STACK.md` — provider API shapes, Go patterns, verified 2026-03-01
- Internal codebase: `.planning/research/PITFALLS.md` — CreateFormFile pitfall, Deepgram auth pitfall, verified 2026-03-01
- Internal codebase: `.planning/research/ARCHITECTURE.md` — file structure, data flow, anti-patterns

### Secondary (MEDIUM confidence)
- OpenAI audio transcriptions API: multipart fields, `verbose_json` response structure with `text`, `segments`, `avg_logprob`, `no_speech_prob` — verified via WebSearch + official URL
- Groq Whisper API: base URL `https://api.groq.com/openai/v1/audio/transcriptions`, multipart identical to OpenAI, `whisper-large-v3` model — verified via WebSearch + Groq docs page
- Deepgram pre-recorded API: `https://api.deepgram.com/v1/listen`, binary body, `Authorization: Token`, `results.channels[0].alternatives[0].transcript` path — verified via WebFetch of official docs page
- Go exponential backoff + jitter pattern (stdlib only) — WebSearch + multiple community sources agreeing

### Tertiary (LOW confidence)
- Groq `verbose_json` segment fields (avg_logprob, no_speech_prob available) — WebSearch only; requires validation against real Groq response

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — pure stdlib; all packages from Go 1.22 standard library already used in project
- Architecture: HIGH — openai.go/deepgram.go/retry.go structure derived directly from existing codebase patterns + prior research
- Pitfalls: HIGH — CreateFormFile, Deepgram auth, multipart Close, empty channels all verified against official docs/issues
- Provider API shapes: MEDIUM — endpoints and response shapes confirmed via multiple sources; not HIGH due to some doc pages returning 403/404

**Research date:** 2026-03-01
**Valid until:** 2026-04-01 (API endpoints are stable; Groq/Deepgram model names evolve faster — verify defaults if >30 days old)
