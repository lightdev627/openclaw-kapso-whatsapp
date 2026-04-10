# Phase 1: Foundation - Research

**Researched:** 2026-03-01
**Domain:** Go config extension, interface design, HTTP media download
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Env var naming:**
- Use `KAPSO_TRANSCRIBE_*` prefix for all transcription env vars (consistent with existing `KAPSO_*` convention)
- Full list: `KAPSO_TRANSCRIBE_PROVIDER`, `KAPSO_TRANSCRIBE_API_KEY`, `KAPSO_TRANSCRIBE_MODEL`, `KAPSO_TRANSCRIBE_LANGUAGE`, `KAPSO_TRANSCRIBE_MAX_AUDIO_SIZE`
- Local provider paths: `KAPSO_TRANSCRIBE_BINARY_PATH`, `KAPSO_TRANSCRIBE_MODEL_PATH`
- Separate `KAPSO_TRANSCRIBE_API_KEY` — no fallback to `KAPSO_API_KEY` (different services)
- Every config field gets an env override — no exceptions

**Provider strings:**
- Service names: `"openai"`, `"groq"`, `"deepgram"`, `"local"` — four valid strings, nothing else
- Case-insensitive matching (lowercase internally, matches existing `resolveMode()` pattern in config.go)
- No aliases — `"whisper"`, `"nova"`, etc. are rejected
- Model field is optional; each provider has a hardcoded default (`whisper-1`, `whisper-large-v3`, `nova-3`)

**Startup error behavior:**
- Unknown provider string → crash at startup with clear error (matches success criteria SC-3)
- Cloud provider set but API key missing → crash: `"provider 'groq' requires KAPSO_TRANSCRIBE_API_KEY"`
- Local provider: verify binary exists and is executable during `New(cfg)` — crash if not found
- Provider empty/missing → transcription disabled, log info: `"transcription disabled (no provider configured)"`
- Philosophy: fail fast on config errors, silent operation when intentionally disabled

**Config defaults:**
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

### Deferred Ideas (OUT OF SCOPE)

None — discussion stayed within phase scope
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| CONF-01 | `[transcribe]` TOML section with provider, api_key, model, language, max_audio_size | Extend `Config` struct with `Transcribe TranscribeConfig` field; mirror existing `toml:"field_name"` struct tag pattern |
| CONF-02 | Env overrides: `KAPSO_TRANSCRIBE_PROVIDER`, `KAPSO_TRANSCRIBE_API_KEY`, etc. | Extend `applyEnv()` with the same `if v := os.Getenv(...)` block pattern |
| CONF-03 | 3-tier precedence preserved: defaults < file < env | Already guaranteed by existing `defaults()` → `toml.DecodeFile` → `applyEnv()` call order |
| CONF-04 | Empty/missing provider = transcription disabled (backward compatible) | `New(cfg)` returns `(nil, nil)` when provider is empty — no crash, no config change |
| CONF-05 | Default language support for Spanish and English (auto-detect when empty) | `language` field defaults to `""` — Whisper/Deepgram both support multilingual auto-detect |
| TRNS-01 | `Transcriber` interface with single method: `Transcribe(ctx, audio []byte, mimeType string) (string, error)` | Standard Go interface pattern; factory `New(cfg) (Transcriber, error)` returns nil when disabled |
| MEDL-01 | `DownloadMedia(url string) ([]byte, error)` method on Kapso client | Extends existing `kapso.Client`; follows same pattern as `GetMediaURL` |
| MEDL-02 | Authenticates with existing API key header (`X-API-Key`) | Header already set on every request in `kapso.Client` — same pattern applies |
| MEDL-03 | Enforces configurable max size limit (default 25MB) via `io.LimitReader` | Use `io.LimitReader(resp.Body, maxBytes+1)` then check read length; not Content-Length header |
| MEDL-04 | Downloads immediately at call site — media URLs expire in ~5 minutes | No buffering/deferred download; call happens synchronously in message processing path |
| WIRE-01 | Build Transcriber from config at startup in main.go (nil if disabled) | `transcriber, err := transcribe.New(cfg.Transcribe)` after config.Load(); log.Fatalf on error; pass nil if disabled |
| TEST-04 | Media download test with size limit enforcement | Use `httptest.NewServer` mock with known-size response; verify limit error and successful download |
</phase_requirements>

---

## Summary

Phase 1 delivers three purely additive changes to a well-structured Go codebase: a new TOML config section, a single-method interface, and one new method on an existing HTTP client. The existing code is the best documentation — every pattern needed already exists and only needs to be replicated.

The codebase uses Go 1.22, standard library only (plus `gorilla/websocket` and `BurntSushi/toml`), with no frameworks. All the patterns for this phase — config struct extension, env override blocks, case-insensitive normalization, HTTP client injection for testability, `httptest.NewServer` mocks — are already demonstrated in the existing code and simply need to be followed, not invented.

The key risk in this phase is ensuring that a missing or empty provider causes zero behavior change to the existing message flow. The existing `ExtractText` audio case already returns `"[audio] (mime)"` and must continue to do so until Phase 3. `DownloadMedia` and the `Transcriber` interface are inert building blocks — nothing calls them after construction in this phase.

**Primary recommendation:** Clone existing patterns exactly — `applyEnv()` style, `resolveMode()` normalization, `httptest.NewServer` with `rewriteTransport` for tests. No new patterns needed.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/BurntSushi/toml` | v1.6.0 | TOML config parsing | Already in use; `toml.DecodeFile` is the existing config loader |
| `io` (stdlib) | Go 1.22 | `io.LimitReader` for size capping | Correct approach per locked decision MEDL-03 |
| `net/http` (stdlib) | Go 1.22 | HTTP client for media download | Already used in `kapso.Client` |
| `net/http/httptest` (stdlib) | Go 1.22 | Mock HTTP server for tests | Already used in `extract_test.go` for similar pattern |
| `os` (stdlib) | Go 1.22 | Env var reads, binary existence check | Already used in `applyEnv()` and `config.go` |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `context` (stdlib) | Go 1.22 | `Transcribe` method signature | Required in interface signature per TRNS-01 |
| `fmt` (stdlib) | Go 1.22 | Error wrapping with `%w` | All error paths in the codebase use `fmt.Errorf("context: %w", err)` |
| `log` (stdlib) | Go 1.22 | All logging | Project convention — no framework |
| `strings` (stdlib) | Go 1.22 | `strings.ToLower()` for provider normalization | Existing `resolveMode()` uses this |
| `os/exec` (stdlib) | Go 1.22 | Binary existence check for `"local"` provider | `exec.LookPath("whisper-cli")` or `os.Stat(binaryPath)` |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `io.LimitReader` | Checking `Content-Length` header | Content-Length can be absent, spoofed, or lie — `io.LimitReader` enforces at read time. Locked decision. |
| Standard `log` | `slog`, `zerolog`, `zap` | Project convention is standard log — don't change it |
| Separate `internal/transcribe/` package | Inline in `internal/config/` | Interface + factory in their own package is cleaner and enables Phase 2 providers to implement the interface |

**Installation:** No new dependencies needed. All required packages are stdlib or already in `go.mod`.

---

## Architecture Patterns

### Recommended Project Structure
```
internal/
├── config/
│   └── config.go          # Add TranscribeConfig struct + applyEnv block + defaults
├── kapso/
│   └── client.go          # Add DownloadMedia method
└── transcribe/
    └── transcribe.go      # Transcriber interface + New() factory
cmd/kapso-whatsapp-poller/
    └── main.go            # Wire: transcriber, err := transcribe.New(cfg.Transcribe)
```

### Pattern 1: Config Struct Extension
**What:** Add `Transcribe TranscribeConfig` to the top-level `Config` struct, with `TranscribeConfig` fields matching the TOML key names exactly.
**When to use:** Every new config section in this codebase follows this pattern.
**Example:**
```go
// internal/config/config.go — extend Config struct
type Config struct {
    Kapso     KapsoConfig     `toml:"kapso"`
    Delivery  DeliveryConfig  `toml:"delivery"`
    Webhook   WebhookConfig   `toml:"webhook"`
    Gateway   GatewayConfig   `toml:"gateway"`
    State     StateConfig     `toml:"state"`
    Security  SecurityConfig  `toml:"security"`
    Transcribe TranscribeConfig `toml:"transcribe"` // NEW
}

type TranscribeConfig struct {
    Provider     string `toml:"provider"`
    APIKey       string `toml:"api_key"`
    Model        string `toml:"model"`
    Language     string `toml:"language"`
    MaxAudioSize int64  `toml:"max_audio_size"`
    BinaryPath   string `toml:"binary_path"`
    ModelPath    string `toml:"model_path"`
    Timeout      int    `toml:"timeout"`
}
```

### Pattern 2: Env Override Block
**What:** Extend `applyEnv()` with a block for transcription env vars using the identical `if v := os.Getenv("KEY"); v != "" { cfg.X.Y = v }` pattern.
**When to use:** Every env override in this codebase uses this exact pattern — do not deviate.
**Example:**
```go
// internal/config/config.go — extend applyEnv()
// Transcribe overrides.
if v := os.Getenv("KAPSO_TRANSCRIBE_PROVIDER"); v != "" {
    cfg.Transcribe.Provider = strings.ToLower(v)
}
if v := os.Getenv("KAPSO_TRANSCRIBE_API_KEY"); v != "" {
    cfg.Transcribe.APIKey = v
}
if v := os.Getenv("KAPSO_TRANSCRIBE_MODEL"); v != "" {
    cfg.Transcribe.Model = v
}
if v := os.Getenv("KAPSO_TRANSCRIBE_LANGUAGE"); v != "" {
    cfg.Transcribe.Language = v
}
if v := os.Getenv("KAPSO_TRANSCRIBE_MAX_AUDIO_SIZE"); v != "" {
    if n, err := strconv.ParseInt(v, 10, 64); err == nil {
        cfg.Transcribe.MaxAudioSize = n
    }
}
if v := os.Getenv("KAPSO_TRANSCRIBE_BINARY_PATH"); v != "" {
    cfg.Transcribe.BinaryPath = v
}
if v := os.Getenv("KAPSO_TRANSCRIBE_MODEL_PATH"); v != "" {
    cfg.Transcribe.ModelPath = v
}
```

### Pattern 3: Config Defaults
**What:** Add transcription defaults inside the `defaults()` function.
**When to use:** Every config section with non-zero defaults uses this function.
**Example:**
```go
// internal/config/config.go — extend defaults()
Transcribe: TranscribeConfig{
    MaxAudioSize: 25 * 1024 * 1024, // 25MB
    BinaryPath:   "whisper-cli",
    Timeout:      30,
},
```

### Pattern 4: Transcriber Interface + Factory
**What:** Single-method interface in `internal/transcribe/transcribe.go`. Factory `New()` returns `(Transcriber, error)` — nil interface value and nil error when provider is empty (disabled). Returns non-nil error only on misconfiguration.
**When to use:** Interface enables Phase 2+ providers to implement it without touching this package.
**Example:**
```go
// internal/transcribe/transcribe.go
package transcribe

import (
    "context"
    "fmt"
    "log"
    "os/exec"
    "strings"

    "github.com/Enriquefft/openclaw-kapso-whatsapp/internal/config"
)

// Transcriber converts raw audio bytes to text.
type Transcriber interface {
    Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error)
}

// New builds a Transcriber from config. Returns (nil, nil) when provider is
// empty — transcription is intentionally disabled. Returns an error if the
// provider string is unrecognized or required config is missing.
func New(cfg config.TranscribeConfig) (Transcriber, error) {
    provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
    if provider == "" {
        log.Printf("transcription disabled (no provider configured)")
        return nil, nil
    }

    switch provider {
    case "openai", "groq", "deepgram":
        if cfg.APIKey == "" {
            return nil, fmt.Errorf("provider %q requires KAPSO_TRANSCRIBE_API_KEY", provider)
        }
        // Phase 2 will return real implementations here.
        return nil, fmt.Errorf("provider %q not yet implemented (Phase 2)", provider)
    case "local":
        path := cfg.BinaryPath
        if path == "" {
            path = "whisper-cli"
        }
        if _, err := exec.LookPath(path); err != nil {
            return nil, fmt.Errorf("local provider: binary %q not found in PATH: %w", path, err)
        }
        // Phase 3 will return real implementation here.
        return nil, fmt.Errorf("local provider not yet implemented (Phase 3)")
    default:
        return nil, fmt.Errorf("unknown transcription provider %q (valid: openai, groq, deepgram, local)", provider)
    }
}
```

**Important:** For Phase 1, the factory validates configuration correctness and crashes on misconfiguration, but returns an error for valid-but-unimplemented providers. The planner should decide: either keep the "not yet implemented" stub errors or only validate known-good config shapes and defer actual construction to Phase 2. Given the success criteria says "unknown provider string returns an error at startup", the switch + default case is mandatory. The implemented cases can be left as stubs that will be filled in Phase 2.

### Pattern 5: DownloadMedia Method
**What:** New method on `kapso.Client` following the identical pattern of `GetMediaURL`. Uses `io.LimitReader` to cap body reads. Takes the URL directly (caller already has it from `GetMediaURL`).
**When to use:** Called from Phase 3+ when audio messages need to be downloaded for transcription.
**Example:**
```go
// internal/kapso/client.go — add DownloadMedia method
// DownloadMedia fetches audio bytes from a media URL. It enforces a size
// limit using io.LimitReader — responses exceeding maxBytes return an error.
func (c *Client) DownloadMedia(url string, maxBytes int64) ([]byte, error) {
    req, err := http.NewRequest("GET", url, nil)
    if err != nil {
        return nil, fmt.Errorf("create request: %w", err)
    }
    req.Header.Set("X-API-Key", c.APIKey)

    resp, err := c.HTTPClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("download media: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
        return nil, fmt.Errorf("media download error (status %d): %s", resp.StatusCode, string(body))
    }

    // Read up to maxBytes+1 to detect oversized responses.
    limited := io.LimitReader(resp.Body, maxBytes+1)
    data, err := io.ReadAll(limited)
    if err != nil {
        return nil, fmt.Errorf("read media body: %w", err)
    }
    if int64(len(data)) > maxBytes {
        return nil, fmt.Errorf("media response exceeds size limit (%d bytes)", maxBytes)
    }
    return data, nil
}
```

### Pattern 6: Wiring in main.go
**What:** After `cfg.Validate()`, construct the transcriber (nil if disabled, fatal if misconfigured).
**When to use:** WIRE-01 mandates this. Transcriber is constructed once at startup.
**Example:**
```go
// cmd/kapso-whatsapp-poller/main.go — after cfg.Validate()
transcriber, err := transcribe.New(cfg.Transcribe)
if err != nil {
    log.Fatalf("transcription config error: %v", err)
}
// transcriber is nil when disabled — Phase 3 will use it
_ = transcriber
```

### Anti-Patterns to Avoid
- **Checking `Content-Length` for size limiting:** Header can be absent or lie. Always use `io.LimitReader`.
- **Falling back `KAPSO_TRANSCRIBE_API_KEY` to `KAPSO_API_KEY`:** Locked decision — different services, no fallback.
- **Global variables for Transcriber:** Project convention is no globals — store in structs or pass as arguments.
- **`log.Fatal` inside `New()`:** `New()` should return an error; caller in `main.go` calls `log.Fatalf`. Keep library code error-returning.
- **Adding `no_speech_threshold` to config now:** Explicitly deferred to Phase 4 (YAGNI).
- **Implementing providers in Phase 1:** Factory returns `(nil, nil)` for disabled case; known providers can crash with "not yet implemented" until Phase 2 fills them in. Alternatively, the factory could accept the unimplemented providers as valid (return nil Transcriber with nil error), which would allow cleaner Phase 2 addition. This is a discretion area — see Open Questions.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Body size limiting | Custom byte-counting reader | `io.LimitReader` (stdlib) | Correct, tested, handles all edge cases; locked by MEDL-03 |
| Config file parsing | Custom TOML parser | `BurntSushi/toml` (already in go.mod) | Already the project's config library |
| HTTP mock server | Custom test server | `net/http/httptest.NewServer` (stdlib) | Already demonstrated in `extract_test.go` |
| Binary path lookup | `os.Stat` loop | `os/exec.LookPath` (stdlib) | Handles PATH resolution correctly |

**Key insight:** Every problem in this phase has a stdlib or already-imported solution. The challenge is codebase consistency, not library selection.

---

## Common Pitfalls

### Pitfall 1: LimitReader Off-By-One
**What goes wrong:** `io.LimitReader(resp.Body, maxBytes)` reads exactly `maxBytes` without error even if the body is larger. A body of exactly `maxBytes` and one of `maxBytes+1` both read successfully — you can't tell which happened.
**Why it happens:** `io.LimitReader` is a cap, not a trip-wire.
**How to avoid:** Read `maxBytes+1` bytes, then check `len(data) > maxBytes`. If true, the body was oversized.
**Warning signs:** Test passes for exact-limit payloads but never triggers the error path.

### Pitfall 2: Interface Nil vs. Typed Nil
**What goes wrong:** Returning a typed nil pointer (`(*concreteType)(nil)`) from `New()` when transcription is disabled. The caller checks `transcriber == nil` and gets `false` even though the pointer is nil.
**Why it happens:** Go interface comparison: a non-nil interface holding a nil concrete value is not nil.
**How to avoid:** When provider is empty, return `nil, nil` (untyped nil), not a typed nil pointer.
**Warning signs:** `if transcriber != nil { transcriber.Transcribe(...) }` panics at nil dereference.

### Pitfall 3: TOML Zero-Value Masking Defaults
**What goes wrong:** If the user's config file has `[transcribe]` with `max_audio_size = 0`, `toml.DecodeFile` sets the field to 0, overwriting the `defaults()` value of 25MB.
**Why it happens:** TOML decodes into an existing struct — zero values in the file replace non-zero defaults.
**How to avoid:** For the size limit specifically, validate in `Validate()` and reset to default if zero. Example: `if cfg.Transcribe.MaxAudioSize <= 0 { cfg.Transcribe.MaxAudioSize = 25 * 1024 * 1024 }`.
**Warning signs:** A user with an empty `[transcribe]` section gets 0-byte limit and all downloads fail.

### Pitfall 4: Provider Normalization Timing
**What goes wrong:** Provider string is normalized to lowercase in `applyEnv()` for env var path, but not when read from TOML. Factory `New()` receives `"OpenAI"` from TOML and fails to match `"openai"` in the switch.
**Why it happens:** TOML decodes raw string values without normalization.
**How to avoid:** Normalize in `New()` with `strings.ToLower(strings.TrimSpace(cfg.Provider))`, or normalize in `Validate()`. Both work — choose one place.
**Warning signs:** `KAPSO_TRANSCRIBE_PROVIDER=openai` works but `provider = "OpenAI"` in TOML fails.

### Pitfall 5: Transcriber Nil Check in Existing Code
**What goes wrong:** Phase 3 modifies `ExtractText` to call `transcriber.Transcribe(...)` but misses the nil guard, crashing when transcription is disabled.
**Why it happens:** The Transcriber is optional — callers must nil-check.
**How to avoid:** Phase 1 establishes the pattern in main.go with `_ = transcriber` — Phase 3 must do `if transcriber != nil` before calling. Document this in code comments on the `New()` function.
**Warning signs:** Daemon crashes on audio messages when transcription is disabled.

---

## Code Examples

Verified patterns from official sources (the existing codebase is the authoritative source):

### io.LimitReader Pattern (stdlib)
```go
// Source: Go stdlib io package, confirmed against Go 1.22 docs
// Read up to maxBytes+1; if we get more, the body was oversized.
limited := io.LimitReader(resp.Body, maxBytes+1)
data, err := io.ReadAll(limited)
if err != nil {
    return nil, fmt.Errorf("read body: %w", err)
}
if int64(len(data)) > maxBytes {
    return nil, fmt.Errorf("response exceeds size limit (%d bytes)", maxBytes)
}
```

### httptest.NewServer Pattern (from extract_test.go)
```go
// Source: internal/delivery/extract_test.go — existing codebase
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // Write known-size response for limit testing
    w.Write(bytes.Repeat([]byte("x"), testSize))
}))
defer srv.Close()

client := &kapso.Client{
    APIKey:     "test-key",
    HTTPClient: &http.Client{
        Transport: &rewriteTransport{base: srv.URL, wrapped: http.DefaultTransport},
    },
}
```

### resolveMode Pattern (from config.go)
```go
// Source: internal/config/config.go — existing codebase
// Use exact same pattern for provider normalization
switch strings.ToLower(provider) {
case "openai", "groq", "deepgram", "local":
    return strings.ToLower(provider)
default:
    return "" // or return error
}
```

### Error Wrapping Pattern (from config.go, client.go)
```go
// Source: consistent throughout codebase
return nil, fmt.Errorf("context description: %w", err)
// Never: errors.New() for wrapped errors, or bare string returns
```

---

## State of the Art

| Old Approach | Current Approach | Impact |
|--------------|------------------|--------|
| All config in one flat struct | Nested structs per section with `toml:"section"` tags | New `[transcribe]` section follows exact same pattern |
| Checking HTTP `Content-Length` for size limits | `io.LimitReader` at read time | Locked decision — reliable even when header is absent |

**No deprecated patterns identified** — the existing codebase is clean and current.

---

## Open Questions

1. **Should Phase 1 `New()` accept valid cloud providers as "disabled" or fail fast?**
   - What we know: Success criteria SC-3 says "unknown provider string returns an error at startup". Known providers (`openai`, `groq`, `deepgram`, `local`) are valid strings.
   - What's unclear: If a user configures `provider = "openai"` in Phase 1, before Phase 2 is implemented, should startup fail with "not yet implemented" or silently proceed with a nil transcriber?
   - Recommendation: Return a "not yet implemented" error for Phase 1. This forces Phase 2 to add the real implementation before providers can be used. Avoids silent no-op when the user thinks transcription is active.

2. **Where does `max_audio_size` live in the config — `TranscribeConfig` or `KapsoConfig`?**
   - What we know: CONF-01 and MEDL-03 both reference it. The locked decision places it in `[transcribe]`. `DownloadMedia` takes it as a parameter.
   - What's unclear: Should `DownloadMedia(url, maxBytes)` receive the limit at call time, or should `kapso.Client` hold it internally?
   - Recommendation: Pass `maxBytes` as a parameter to `DownloadMedia`. Keeps `kapso.Client` stateless re: transcription config. Cleaner separation of concerns.

3. **`rewriteTransport` duplication in tests**
   - What we know: `extract_test.go` already defines `rewriteTransport` in package `delivery`. A new test in package `kapso` will need the same helper.
   - What's unclear: Whether to copy or extract to a shared test helper package.
   - Recommendation: Copy it into the new `kapso` test file. Go test helpers shared across packages require a `internal/testutil` package — that's a separate refactor. Keep it local for now.

---

## Sources

### Primary (HIGH confidence)
- Existing codebase: `internal/config/config.go` — `applyEnv()`, `resolveMode()`, `defaults()`, struct tag patterns directly observed
- Existing codebase: `internal/kapso/client.go` — `GetMediaURL()` pattern for `DownloadMedia`
- Existing codebase: `internal/delivery/extract_test.go` — `httptest.NewServer` + `rewriteTransport` test pattern
- Go stdlib documentation (Go 1.22): `io.LimitReader` — standard size-capping approach
- `go.mod`: confirmed dependency set (gorilla/websocket v1.5.3, BurntSushi/toml v1.6.0)

### Secondary (MEDIUM confidence)
- Go interface nil comparison behavior — well-documented in Go spec (typed nil vs untyped nil)
- `os/exec.LookPath` for binary existence verification — standard Go idiom

### Tertiary (LOW confidence)
- None — all claims are based on directly observed code or Go stdlib

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries are already in use in the codebase; no new dependencies
- Architecture: HIGH — patterns are directly observable in existing code, not inferred
- Pitfalls: HIGH — LimitReader off-by-one and typed nil pitfalls are well-known Go patterns; TOML zero-value masking observed from config structure

**Research date:** 2026-03-01
**Valid until:** 2026-04-01 (stable domain — Go stdlib, no fast-moving external APIs in scope)
