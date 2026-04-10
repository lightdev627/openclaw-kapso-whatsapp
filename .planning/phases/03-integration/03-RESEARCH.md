# Phase 3: Integration - Research

**Researched:** 2026-03-01
**Domain:** Go subprocess execution (whisper.cpp + ffmpeg), delivery pipeline wiring, context-scoped temp file cleanup
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Failure behavior**
- When transcription fails for any reason, pipeline receives `[audio] (mime)` with a WARN log — message is never lost
- No WhatsApp notification to sender on failure — silent fallback, consistent with existing media handling patterns
- Use existing provider-level timeout (30s config default) — no separate pipeline timeout needed

**Output formatting**
- Successfully transcribed audio appears as `[voice] <transcript>`
- Distinct from `[audio]` fallback tag so agent knows transcription succeeded
- No language tag in output — language is a config detail, not useful to the agent

**Local whisper.cpp provider**
- `ModelPath` is required config — user must set `model_path` or `KAPSO_TRANSCRIBE_MODEL_PATH`, clear error if missing
- Temp files for OGG->WAV conversion use `os.TempDir()` — standard Go approach, cleaned up on completion and context cancellation
- Validate both `whisper-cli` and `ffmpeg` at startup in `New()` — fail fast, consistent with existing `exec.LookPath` pattern already in the factory

**Audio size limits**
- Oversized audio (>25MB default) skips transcription and falls back to `[audio] (mime)` with WARN log — treated as transcription failure
- Size check happens after download — simpler, always works

### Claude's Discretion
- Exact ffmpeg conversion flags for OGG->WAV
- Whisper-cli invocation flags and output parsing
- Error message wording in logs
- Whether to use a streaming or buffered approach for audio download

### Deferred Ideas (OUT OF SCOPE)
None — discussion stayed within phase scope
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| TRNS-02 | Transcribed audio enters pipeline as `[voice] ` + transcript, identical to typed text | ExtractText audio branch pattern; `[voice] ` prefix in fallback pattern |
| TRNS-03 | Transcription failure falls back to `[audio] (mime)` with log warning (zero message loss) | Fallback pattern in ExtractText audio case; existing `formatMediaMessage` for audio already produces this output |
| LOCL-01 | Local whisper.cpp provider — write audio to temp file, exec whisper-cli with `exec.CommandContext`, capture stdout | whisper-cli stdout output verified; `exec.CommandContext` cleanup pattern documented |
| LOCL-02 | OGG/Opus to WAV conversion via ffmpeg before whisper.cpp processing | ffmpeg flags verified: `-acodec pcm_s16le -ac 1 -ar 16000`; whisper.cpp requires 16-bit WAV |
| LOCL-03 | Configurable binary path and model path for whisper.cpp | `config.TranscribeConfig` already has `BinaryPath` and `ModelPath` fields; factory validates with `exec.LookPath` |
| LOCL-04 | Temp files cleaned up after use (including on context cancellation) | `defer os.RemoveAll(dir)` with `os.MkdirTemp`; `exec.CommandContext` kills subprocess on cancellation |
| WIRE-02 | Pass Transcriber to delivery layer — no new goroutines, transcription synchronous within message processing | `ExtractText` signature change from `(msg, client)` to `(msg, client, tr)` with nil guard; two call sites: poller and webhook |
| WIRE-03 | ExtractText receives optional Transcriber (nil = disabled, current behavior preserved) | nil-check pattern established; all non-audio cases untouched |
| INFR-02 | `context.WithTimeout` per transcription call to prevent pipeline blocking | retryTranscriber already applies `cfg.Timeout` for cloud providers; local provider must apply the same timeout from ctx passed in |
| TEST-02 | Local whisper.cpp provider test with mock exec | `TestHelperProcess` pattern for mocking exec in Go; injectable `execFunc` approach as alternative |
| TEST-03 | Extract integration test with mock transcriber (success + failure fallback) | Mock Transcriber interface with table-driven success/error cases; existing `extract_test.go` patterns apply directly |
</phase_requirements>

## Summary

Phase 3 is the integration phase: it completes the audio transcription pipeline by implementing the local `whisper.cpp` provider and wiring the `Transcriber` into `delivery.ExtractText`. All infrastructure is already in place — the interface, factory, config, retry wrapper, and cloud providers are done. This phase adds the last provider and connects everything end-to-end.

The two primary work areas are: (1) `internal/transcribe/local.go` implementing `localWhisper.Transcribe()` using `exec.CommandContext` for both `ffmpeg` and `whisper-cli`; and (2) modifying `delivery/extract.go` to widen `ExtractText` signature to accept `transcribe.Transcriber` and threading the wiring through `main.go`, `poller/poller.go`, and `webhook/server.go`. The existing test infrastructure in `extract_test.go` and `transcribe/` establishes clear patterns for both.

The biggest technical risks are: (a) whisper-cli stdout parsing — the tool emits diagnostic content to stderr and transcription to stdout, but `-nt` (no-timestamps) has a known quality-degradation bug (#2186 on ggml-org/whisper.cpp); the safer approach is to use `-otxt` (output to file) and read the `.txt` file rather than stdout; (b) temp file leaks when `exec.CommandContext` kills a subprocess — `os.MkdirTemp` with `defer os.RemoveAll` in the Go side handles Go-created files; the ffmpeg and whisper-cli tools clean up their own outputs when given explicit output paths.

**Primary recommendation:** Use `os.MkdirTemp` for a scoped working directory, run ffmpeg writing WAV to that dir, run whisper-cli with `-otxt` writing `.txt` to that dir, read the `.txt` file, and `defer os.RemoveAll(dir)`. This avoids stdout parsing complexity and handles cleanup reliably.

## Standard Stack

### Core (stdlib only — no new imports required)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `os/exec` | Go stdlib | Run ffmpeg and whisper-cli as subprocesses | Already used in factory (`exec.LookPath`); idiomatic Go for external processes |
| `os` | Go stdlib | `MkdirTemp`, `RemoveAll`, `ReadFile`, `WriteFile` | Temp file management; context-cancellation safe when combined with `exec.CommandContext` |
| `context` | Go stdlib | Timeout/cancellation propagation to subprocesses | `exec.CommandContext` kills subprocess when context is done |
| `strings` | Go stdlib | `strings.TrimSpace` for transcript output | whisper-cli stdout/txt file has trailing newlines |
| `fmt` | Go stdlib | Error wrapping | Project convention: `fmt.Errorf("...: %w", err)` |
| `log` | Go stdlib | WARN logging on fallback | Project uses standard `log` package |

### Supporting (already present in project)

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `transcribe.Transcriber` interface | internal | Contract for all providers | Already defined; localWhisper implements it |
| `config.TranscribeConfig` | internal | `BinaryPath`, `ModelPath`, `Language`, `Timeout` fields | Already has all fields for local provider |

### External Binaries (operator-provided, not Go dependencies)

| Binary | Purpose | Validation |
|--------|---------|-----------|
| `ffmpeg` | OGG/Opus → WAV conversion | `exec.LookPath("ffmpeg")` at startup in `New()` |
| `whisper-cli` | Local speech-to-text | `exec.LookPath(cfg.BinaryPath)` already in factory stub |

**No new Go module dependencies.** All stdlib.

## Architecture Patterns

### Recommended Project Structure

```
internal/transcribe/
├── transcribe.go           # Transcriber interface (already exists)
├── transcribe_test.go      # Factory tests (already exists — add local success case)
├── openai.go               # OpenAI/Groq provider (done)
├── deepgram.go             # Deepgram provider (done)
├── retry.go                # retryTranscriber (done)
├── mime.go                 # NormalizeMIME helper (done)
├── local.go                # NEW: localWhisper implementation
└── local_test.go           # NEW: table-driven tests with mock exec

internal/delivery/
├── extract.go              # MODIFY: ExtractText(msg, client, tr) — add Transcriber param + audio branch
└── extract_test.go         # MODIFY: add audio transcription cases (success + failure fallback)

internal/delivery/poller/
└── poller.go               # MODIFY: pass transcriber to ExtractText

internal/delivery/webhook/
└── server.go               # MODIFY: pass transcriber to ExtractText (store on Server struct or pass via closure)

cmd/kapso-whatsapp-poller/
└── main.go                 # MODIFY: replace `_ = transcriber` with actual wiring to delivery layer
```

### Pattern 1: localWhisper Struct and Constructor

**What:** Implements `Transcriber` interface using `exec.CommandContext` for ffmpeg and whisper-cli.

**When to use:** When `provider = "local"` in config.

```go
// Source: internal codebase convention + Go stdlib docs
type localWhisper struct {
    BinaryPath string // path to whisper-cli binary (validated at startup)
    ModelPath  string // path to ggml model file
    Language   string // optional language hint (e.g., "es", "en")
    // execCmd is injectable for testing (nil = use exec.CommandContext)
    execCmd func(ctx context.Context, name string, args ...string) *exec.Cmd
}

func newLocalWhisper(cfg config.TranscribeConfig) (*localWhisper, error) {
    if cfg.ModelPath == "" {
        return nil, fmt.Errorf("local provider requires model_path (set KAPSO_TRANSCRIBE_MODEL_PATH)")
    }
    // BinaryPath validated by exec.LookPath in factory before calling this
    execFn := func(ctx context.Context, name string, args ...string) *exec.Cmd {
        return exec.CommandContext(ctx, name, args...)
    }
    return &localWhisper{
        BinaryPath: cfg.BinaryPath,
        ModelPath:  cfg.ModelPath,
        Language:   cfg.Language,
        execCmd:    execFn,
    }, nil
}
```

### Pattern 2: Temp Directory Lifecycle with defer RemoveAll

**What:** Single `os.MkdirTemp` creates a scoped working directory. Deferred `os.RemoveAll` cleans it up regardless of error path or context cancellation.

**When to use:** Whenever a subprocess needs input/output files. Prefer `MkdirTemp` over individual `CreateTemp` calls so a single `RemoveAll` handles everything.

```go
// Source: Go stdlib docs (os.MkdirTemp, os.RemoveAll)
func (p *localWhisper) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
    dir, err := os.MkdirTemp("", "kapso-whisper-*")
    if err != nil {
        return "", fmt.Errorf("create temp dir: %w", err)
    }
    defer os.RemoveAll(dir) // cleanup on any exit path including context cancellation

    // Write raw audio
    rawPath := filepath.Join(dir, "audio.ogg")
    if err := os.WriteFile(rawPath, audio, 0o600); err != nil {
        return "", fmt.Errorf("write audio file: %w", err)
    }
    // ... ffmpeg conversion + whisper-cli ...
}
```

### Pattern 3: ffmpeg OGG-to-WAV Conversion

**What:** Run ffmpeg to convert OGG/Opus to 16-bit mono WAV at 16kHz — the format whisper.cpp requires.

**Flags verified:** `-acodec pcm_s16le -ac 1 -ar 16000` produces the correct PCM WAV format for ASR. The `-y` flag overwrites without prompting. `-loglevel error` keeps stderr quiet for normal operation.

```go
// Source: ffmpeg docs + WebSearch verified (multiple authoritative sources)
wavPath := filepath.Join(dir, "audio.wav")
cmd := p.execCmd(ctx, "ffmpeg",
    "-y",
    "-loglevel", "error",
    "-i", rawPath,
    "-acodec", "pcm_s16le",
    "-ac", "1",
    "-ar", "16000",
    wavPath,
)
if out, err := cmd.CombinedOutput(); err != nil {
    return "", fmt.Errorf("ffmpeg conversion failed: %w (output: %s)", err, string(out))
}
```

Note: `CombinedOutput()` is preferred over setting `cmd.Stdout`/`cmd.Stderr` separately — it correctly handles pipe lifecycle and avoids the `cmd.Wait()` blocking issue on pipe descriptors.

### Pattern 4: whisper-cli Invocation with -otxt Output

**What:** Run whisper-cli with `-otxt` to write plain text to `<output>.txt`, then read that file. Avoids stdout parsing complexity and the known `-nt` quality-degradation bug.

**Key flags:**
- `-m <model>` — model file path
- `-f <wav>` — input audio file
- `-otxt` — write plain text transcript to `<wav>.txt`
- `-of <dir/output>` — output file prefix (whisper-cli appends `.txt` automatically)
- `-l <lang>` — language hint (omit for auto-detect)

```go
// Source: whisper.cpp CLI README (github.com/ggml-org/whisper.cpp/blob/master/examples/cli/README.md)
outputPrefix := filepath.Join(dir, "transcript") // whisper-cli appends ".txt"
args := []string{
    "-m", p.ModelPath,
    "-f", wavPath,
    "-otxt",
    "-of", outputPrefix,
}
if p.Language != "" {
    args = append(args, "-l", p.Language)
}
cmd := p.execCmd(ctx, p.BinaryPath, args...)
if out, err := cmd.CombinedOutput(); err != nil {
    return "", fmt.Errorf("whisper-cli failed: %w (output: %s)", err, string(out))
}

// Read the generated .txt file
txtPath := outputPrefix + ".txt"
raw, err := os.ReadFile(txtPath)
if err != nil {
    return "", fmt.Errorf("read transcript: %w", err)
}
return strings.TrimSpace(string(raw)), nil
```

### Pattern 5: ExtractText Signature Change

**What:** Widen `ExtractText` from `(msg kapso.Message, client *kapso.Client)` to `(msg kapso.Message, client *kapso.Client, tr transcribe.Transcriber)`. Nil guard preserves existing behavior for all non-audio messages. Audio branch: download, transcribe, `[voice]` prefix, fallback on any error.

**Call sites that must be updated:**
1. `internal/delivery/poller/poller.go:72` — `delivery.ExtractText(msg.Message, p.Client)`
2. `internal/delivery/webhook/server.go:137` — `delivery.ExtractText(msg, s.Client)`
3. All existing tests in `extract_test.go` — all pass `nil` for the new parameter (nil = transcription disabled = existing behavior preserved)

```go
// Source: existing extract.go + project ARCHITECTURE.md pattern
func ExtractText(msg kapso.Message, client *kapso.Client, tr transcribe.Transcriber) (string, bool) {
    // ... text, image, document, video, location cases unchanged ...

    case "audio":
        if msg.Audio == nil {
            return "", false
        }
        // Transcription branch — only when tr is non-nil
        if tr != nil {
            mediaInfo, err := client.GetMediaURL(msg.Audio.ID)
            if err == nil {
                audio, err := client.DownloadMedia(mediaInfo.URL, maxAudioSize)
                if err == nil {
                    if text, err := tr.Transcribe(ctx, audio, msg.Audio.MimeType); err == nil {
                        return "[voice] " + text, true
                    } else {
                        log.Printf("WARN: transcription failed for %s: %v", msg.ID, err)
                    }
                } else {
                    log.Printf("WARN: audio download failed for %s: %v", msg.ID, err)
                }
            }
        }
        // Fallback: existing format (never lost)
        return formatMediaMessage("audio", "", msg.Audio.MimeType, msg.Audio.ID, client), true
}
```

**Important:** `ExtractText` currently has no `context.Context` parameter. For INFR-02 (context.WithTimeout per call), the timeout is already baked into `retryTranscriber` for cloud providers via `cfg.Timeout`. For the local provider, the context passed to `exec.CommandContext` is the subprocess timeout. The caller context flows through if `ExtractText` accepts `ctx context.Context` as a first parameter — but the CONTEXT.md says "use existing provider-level timeout (30s config default) — no separate pipeline timeout needed." Therefore: pass `context.Background()` as the ctx to Transcribe, or thread the caller's context. The simpler approach is `context.Background()` since the pipeline already has graceful shutdown via the daemon's cancel context. See Open Questions.

### Pattern 6: Poller and Webhook Wiring

**Poller:** Store transcriber on the `Poller` struct.

```go
// internal/delivery/poller/poller.go
type Poller struct {
    Client      *kapso.Client
    Interval    time.Duration
    StateDir    string
    StateFile   string
    Transcriber transcribe.Transcriber // nil = disabled
}

// in poll():
text, ok := delivery.ExtractText(msg.Message, p.Client, p.Transcriber)
```

**Webhook Server:** Store transcriber on the `Server` struct.

```go
// internal/delivery/webhook/server.go
type Server struct {
    Addr        string
    VerifyToken string
    AppSecret   string
    Client      *kapso.Client
    Transcriber transcribe.Transcriber // nil = disabled
}

// in handler:
text, ok := delivery.ExtractText(msg, s.Client, s.Transcriber)
```

**main.go:** Replace `_ = transcriber` with actual wiring:

```go
// Build sources
if runPolling {
    sources = append(sources, &poller.Poller{
        Client:      client,
        Interval:    time.Duration(cfg.Delivery.PollInterval) * time.Second,
        StateDir:    cfg.State.Dir,
        StateFile:   filepath.Join(cfg.State.Dir, "last-poll"),
        Transcriber: transcriber, // nil when disabled
    })
}
if mode == "tailscale" || mode == "domain" {
    sources = append(sources, &webhook.Server{
        Addr:        cfg.Webhook.Addr,
        VerifyToken: cfg.Webhook.VerifyToken,
        AppSecret:   cfg.Webhook.Secret,
        Client:      client,
        Transcriber: transcriber, // nil when disabled
    })
}
```

### Pattern 7: Mock exec for Testing (injectable execCmd)

**What:** Inject `execCmd func(ctx, name, args...) *exec.Cmd` on the `localWhisper` struct so tests can substitute a fake binary. This is the same dependency-injection pattern used throughout the project (e.g., `retryTranscriber.sleepFunc`, `mockable now()`).

**Preferred approach for this project:** Injectable function field — avoids the `TestHelperProcess` complexity while fitting the established DI pattern.

```go
// In local_test.go (package transcribe)
func makeLocalWhisper(ffmpegFn, whisperFn func(ctx context.Context, name string, args ...string) *exec.Cmd) *localWhisper {
    // Build a localWhisper with a combined execCmd that dispatches on binary name
    return &localWhisper{
        BinaryPath: "whisper-cli",
        ModelPath:  "/models/ggml-base.bin",
        execCmd: func(ctx context.Context, name string, args ...string) *exec.Cmd {
            if name == "ffmpeg" {
                return ffmpegFn(ctx, name, args...)
            }
            return whisperFn(ctx, name, args...)
        },
    }
}
```

For writing WAV output and transcript files, tests should use `os.MkdirTemp` and set up fixture files, or use `exec.Command("sh", "-c", "echo 'hello world' > $OF.txt")` — the TestHelperProcess pattern is available as a fallback.

### Pattern 8: Mock Transcriber for ExtractText Tests

**What:** Add a simple mock implementing `transcribe.Transcriber` for `extract_test.go`.

```go
// In extract_test.go (package delivery)
type mockTranscriber struct {
    text string
    err  error
}

func (m *mockTranscriber) Transcribe(_ context.Context, _ []byte, _ string) (string, error) {
    return m.text, m.err
}
```

### Anti-Patterns to Avoid

- **`-nt` flag for stdout parsing:** Use `-otxt` instead. The `-nt` flag has a known quality-degradation bug (whisper.cpp issue #2186, May 2024) that silently skips audio segments.
- **`exec.Command` instead of `exec.CommandContext`:** Always use `exec.CommandContext` so ffmpeg and whisper-cli are killed when the daemon shuts down.
- **Individual `os.CreateTemp` per file:** Use `os.MkdirTemp` for the working directory and place all temp files inside it. One `defer os.RemoveAll(dir)` handles everything.
- **`cmd.Stdout = &bytes.Buffer{}`:** Use `cmd.CombinedOutput()` or `cmd.Output()` — these handle pipe lifecycle correctly and avoid the `Wait()` blocking issue.
- **Nil Transcriber panic:** Always add `if tr == nil` guard before the audio transcription branch in `ExtractText`.
- **Skipping ffmpeg for non-OGG audio:** whisper.cpp requires 16-bit WAV at 16kHz. Always run ffmpeg conversion regardless of input MIME type — it handles WAV-to-WAV re-encoding if needed.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Subprocess timeout | Custom timer goroutine | `exec.CommandContext` with ctx that has deadline | `exec.CommandContext` kills the subprocess when ctx is done; custom goroutines can leak |
| Temp file cleanup on cancellation | Signal handlers, cleanup goroutines | `defer os.RemoveAll(dir)` + `exec.CommandContext` | `defer` runs on normal return and on goroutine exit from ctx cancellation kill; subprocess killed by Go runtime |
| Audio format detection | MIME sniffing, file header inspection | Always run ffmpeg conversion | ffmpeg handles all OGG/Opus variants; whisper.cpp requires exact WAV format — conversion is cheap |
| Transcript output parsing | stdout line parsing, regex | Write to `-otxt` file, read with `os.ReadFile` | Avoids `-nt` bug, no streaming complexity, no pipe synchronization needed |

**Key insight:** `exec.CommandContext` + `defer os.RemoveAll` is the complete solution for subprocess management and temp file cleanup. No additional infrastructure is needed.

## Common Pitfalls

### Pitfall 1: whisper-cli `-nt` Bug Skips Audio Silently

**What goes wrong:** Using `-nt` (no timestamps) for stdout parsing causes some audio segments to be silently skipped without error. Transcript appears shorter than expected with no indication of what was dropped.

**Why it happens:** Known regression in whisper.cpp (issue #2186, introduced around commit `f7908f9`, reported May 2024). The bug specifically affects timestamp-disabled mode.

**How to avoid:** Use `-otxt` flag to write transcript to a file. Read the `.txt` file with `os.ReadFile`. This path is unaffected by the `-nt` bug.

**Warning signs:** Transcripts noticeably shorter than expected; comparing stdout vs file output shows discrepancy.

### Pitfall 2: Nil Transcriber Panic in ExtractText

**What goes wrong:** `ExtractText` is called with `tr = nil` (transcription disabled) and the audio branch calls `tr.Transcribe(...)` without a nil guard — panic at runtime.

**Why it happens:** The new audio branch adds a `tr.Transcribe()` call. If the nil check is missing or placed after the call, the zero value of an interface panics.

**How to avoid:** Add `if tr != nil` as the first check in the audio case, before any other logic. All existing tests call `ExtractText(msg, nil)` with two arguments — updating them to `ExtractText(msg, nil, nil)` automatically tests the nil path.

**Warning signs:** Panic in test output when running `TestExtractText_Audio` after signature change without adding nil guard.

### Pitfall 3: Missing ffmpeg at Runtime (Not Caught at Startup)

**What goes wrong:** `ffmpeg` is not present on the operator's system. The factory validates `whisper-cli` but not `ffmpeg`. Every audio message fails at transcription time with an opaque exec error.

**Why it happens:** The factory stub already validates `BinaryPath` (whisper-cli) but the CONTEXT.md decision requires `ffmpeg` also be validated at startup in `New()`.

**How to avoid:** In the `"local"` case of `transcribe.New()`, call `exec.LookPath("ffmpeg")` immediately after `exec.LookPath(binaryPath)`. Return a clear error: `"local provider requires ffmpeg in PATH"`.

**Warning signs:** `exec: "ffmpeg": executable file not found in $PATH` errors appearing in runtime logs, not startup logs.

### Pitfall 4: Temp File Leaks When Subprocess Hangs

**What goes wrong:** If `cmd.Wait()` blocks indefinitely (subprocess holds open pipe descriptors), the function never returns, `defer os.RemoveAll(dir)` never runs, and temp files accumulate.

**Why it happens:** `cmd.CombinedOutput()` waits for the subprocess to exit. If the subprocess spawns children that hold open file descriptors, `Wait()` can block even after the parent exits.

**How to avoid:** Always use `exec.CommandContext` so the Go runtime sends `SIGKILL` when the deadline expires. Set a reasonable timeout (30s matches existing config default). `CombinedOutput()` respects the context kill.

**Warning signs:** Growing number of `kapso-whisper-*` directories in `/tmp`; goroutine count increasing with audio message volume.

### Pitfall 5: maxAudioSize Not Available in ExtractText

**What goes wrong:** `ExtractText` needs to pass `maxAudioSize` to `client.DownloadMedia()` but `TranscribeConfig.MaxAudioSize` is not currently accessible in the `delivery` package.

**Why it happens:** `ExtractText` today only takes `msg` and `client`. Adding a `Transcriber` parameter is required — but `MaxAudioSize` must also reach the function somehow.

**How to avoid two options:**
1. Include `maxAudioSize int64` as a parameter to `ExtractText` alongside the Transcriber.
2. Wrap `client` and `maxAudioSize` together in a small struct, or store `maxAudioSize` on the `Transcriber` wrapper.

The simplest approach consistent with project patterns: add `maxAudioSize int64` as a fourth parameter to `ExtractText`. This keeps the function pure (no stored state) and makes the size limit explicit at the call site. The caller (poller/webhook/main) has `cfg.Transcribe.MaxAudioSize` available.

**Warning signs:** Compilation error: `client.DownloadMedia` called without the required `maxBytes` argument.

### Pitfall 6: ExtractText Context Availability

**What goes wrong:** `ExtractText` is a pure function with no `context.Context` parameter. `client.DownloadMedia` and `tr.Transcribe` need a context for timeout/cancellation.

**Why it happens:** The existing function signature `ExtractText(msg, client)` was designed before async IO was added.

**How to avoid:** Pass `context.Background()` to the Transcribe call for now, since the CONTEXT.md decision says "no separate pipeline timeout needed" (the 30s timeout is in the provider/retry wrapper). If the daemon shuts down, the process exits; the transcription calls will be killed by Go's process exit cleanup. For a future phase, `ExtractText` could gain a ctx parameter.

**Warning signs:** Context-related timeouts not behaving as expected if provider timeout is not set; the `retryTranscriber` already applies timeout from config so this is acceptable.

## Code Examples

### Complete localWhisper.Transcribe (reference implementation shape)

```go
// internal/transcribe/local.go
// Source: internal codebase patterns + Go stdlib (os, os/exec)

type localWhisper struct {
    BinaryPath string
    ModelPath  string
    Language   string
    execCmd    func(ctx context.Context, name string, args ...string) *exec.Cmd
}

func (p *localWhisper) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
    dir, err := os.MkdirTemp("", "kapso-whisper-*")
    if err != nil {
        return "", fmt.Errorf("create temp dir: %w", err)
    }
    defer os.RemoveAll(dir)

    // Write raw audio
    rawPath := filepath.Join(dir, "audio.ogg")
    if err := os.WriteFile(rawPath, audio, 0o600); err != nil {
        return "", fmt.Errorf("write audio: %w", err)
    }

    // Convert to 16kHz mono WAV (whisper.cpp requirement)
    wavPath := filepath.Join(dir, "audio.wav")
    ffmpegCmd := p.execCmd(ctx, "ffmpeg",
        "-y", "-loglevel", "error",
        "-i", rawPath,
        "-acodec", "pcm_s16le", "-ac", "1", "-ar", "16000",
        wavPath,
    )
    if out, err := ffmpegCmd.CombinedOutput(); err != nil {
        return "", fmt.Errorf("ffmpeg: %w (output: %s)", err, strings.TrimSpace(string(out)))
    }

    // Run whisper-cli, write transcript to file
    outputPrefix := filepath.Join(dir, "transcript")
    wArgs := []string{"-m", p.ModelPath, "-f", wavPath, "-otxt", "-of", outputPrefix}
    if p.Language != "" {
        wArgs = append(wArgs, "-l", p.Language)
    }
    whisperCmd := p.execCmd(ctx, p.BinaryPath, wArgs...)
    if out, err := whisperCmd.CombinedOutput(); err != nil {
        return "", fmt.Errorf("whisper-cli: %w (output: %s)", err, strings.TrimSpace(string(out)))
    }

    // Read transcript
    raw, err := os.ReadFile(outputPrefix + ".txt")
    if err != nil {
        return "", fmt.Errorf("read transcript: %w", err)
    }
    return strings.TrimSpace(string(raw)), nil
}
```

### factory.go local case update

```go
// internal/transcribe/transcribe.go — "local" case in New()
// Source: existing factory pattern in transcribe.go
case "local":
    binaryPath := cfg.BinaryPath
    if binaryPath == "" {
        binaryPath = "whisper-cli"
    }
    if _, err := exec.LookPath(binaryPath); err != nil {
        return nil, fmt.Errorf("local provider binary %q not found: %w", binaryPath, err)
    }
    if _, err := exec.LookPath("ffmpeg"); err != nil {
        return nil, fmt.Errorf("local provider requires ffmpeg in PATH: %w", err)
    }
    if cfg.ModelPath == "" {
        return nil, fmt.Errorf("local provider requires model_path (set KAPSO_TRANSCRIBE_MODEL_PATH)")
    }
    return newLocalWhisper(cfg)
    // NOTE: local provider NOT wrapped in retryTranscriber (local failures are not transient)
```

### ExtractText signature (after change)

```go
// internal/delivery/extract.go
// Source: existing extract.go + project ARCHITECTURE.md
func ExtractText(msg kapso.Message, client *kapso.Client, tr transcribe.Transcriber, maxAudioSize int64) (string, bool) {
    // ... text, image, document cases unchanged ...
    case "audio":
        if msg.Audio == nil {
            return "", false
        }
        if tr != nil {
            if media, err := client.GetMediaURL(msg.Audio.ID); err == nil {
                if audio, err := client.DownloadMedia(media.URL, maxAudioSize); err == nil {
                    if text, err := tr.Transcribe(context.Background(), audio, msg.Audio.MimeType); err == nil {
                        return "[voice] " + text, true
                    } else {
                        log.Printf("WARN: transcription failed for message %s: %v", msg.ID, err)
                    }
                } else {
                    log.Printf("WARN: audio download failed for message %s: %v", msg.ID, err)
                }
            }
        }
        return formatMediaMessage("audio", "", msg.Audio.MimeType, msg.Audio.ID, client), true
    // ...
```

### mock Transcriber for extract_test.go

```go
// internal/delivery/extract_test.go
type mockTranscriber struct {
    text string
    err  error
}

func (m *mockTranscriber) Transcribe(_ context.Context, _ []byte, _ string) (string, error) {
    return m.text, m.err
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `whisper-cli` stdout with `-nt` | `-otxt` flag + read file | whisper.cpp issue #2186 (2024) | Avoids audio segment skipping bug; more reliable transcript capture |
| `exec.Command` for subprocesses | `exec.CommandContext` | Go 1.7+ (standard since) | Subprocess killed on context cancellation; prevents goroutine leaks |
| Individual `os.CreateTemp` per file | `os.MkdirTemp` + `RemoveAll` | Go 1.16+ (`MkdirTemp` added) | Single defer handles all temp files; safer cleanup |

**Deprecated/outdated:**
- `ioutil.TempDir` / `ioutil.TempFile`: Replaced by `os.MkdirTemp` / `os.CreateTemp` in Go 1.16. Project uses Go 1.22 — use `os` package directly.

## Open Questions

1. **Context propagation in ExtractText**
   - What we know: `ExtractText` has no `ctx` parameter today. CONTEXT.md says "no separate pipeline timeout needed — use existing provider-level timeout (30s)."
   - What's unclear: Using `context.Background()` means download + transcription ignore daemon shutdown signal. If the daemon receives SIGTERM while transcribing, the in-flight call will not be cancelled — it will run to completion (or timeout after 30s).
   - Recommendation: Use `context.Background()` for Phase 3 (matches CONTEXT.md decision). Flag for Phase 4 if pipeline latency on shutdown becomes a concern. The retryTranscriber's timeout ensures the call never blocks indefinitely.

2. **maxAudioSize parameter placement**
   - What we know: `client.DownloadMedia` requires `maxBytes int64`; `ExtractText` doesn't currently receive this value.
   - What's unclear: Whether to add it as a 4th parameter to `ExtractText` or to store it on the Poller/Server structs.
   - Recommendation: Add `maxAudioSize int64` as a 4th parameter to `ExtractText`. Pure function, no hidden state. Both call sites (poller, webhook) have access to `cfg.Transcribe.MaxAudioSize` from main.go.

3. **whisper-cli `-of` flag behavior across versions**
   - What we know: `-of <prefix>` sets the output file prefix; whisper-cli appends `.txt` when `-otxt` is used. Documented in official README.
   - What's unclear: Whether all commonly installed versions of whisper-cli (e.g., those packaged by NixOS/homebrew) support `-of`. Older builds used `-o` instead.
   - Recommendation: Use `-of` (current standard). Document the flag in the local provider's error messages. If an operator reports issues, they can set the binary path to a wrapper script.

## Sources

### Primary (HIGH confidence)
- Direct codebase read: `internal/transcribe/transcribe.go`, `internal/delivery/extract.go`, `internal/delivery/poller/poller.go`, `internal/delivery/webhook/server.go`, `cmd/kapso-whatsapp-poller/main.go`, `internal/kapso/types.go`, `internal/config/config.go`
- Go stdlib docs (exec package): https://pkg.go.dev/os/exec — `CommandContext`, `CombinedOutput`, `WaitDelay`
- whisper.cpp CLI README: https://github.com/ggml-org/whisper.cpp/blob/master/examples/cli/README.md — `-otxt`, `-of`, `-m`, `-l` flags verified
- `.planning/research/ARCHITECTURE.md` — architecture decisions and ExtractText patterns
- `.planning/research/PITFALLS.md` — temp file leak and pipeline blocking pitfalls

### Secondary (MEDIUM confidence)
- WebSearch: ffmpeg OGG→WAV conversion flags — `-acodec pcm_s16le -ac 1 -ar 16000` confirmed by multiple authoritative sources (official ffmpeg docs, linuxconfig.org, multiple tutorials)
- WebSearch: Go mock exec pattern — `TestHelperProcess` pattern documented at npf.io and stdlib exec_test.go; injectable function field alternative confirmed as idiomatic

### Tertiary (LOW confidence — flag for validation)
- whisper-cli `-nt` quality regression (issue #2186): WebSearch only; official GitHub issue link referenced but not directly verified in this session. Confidence is HIGH that `-otxt` avoids the issue regardless.
- `-of` flag availability across whisper-cli versions: inferred from README; specific version ranges not confirmed.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — stdlib only, no new dependencies; existing patterns directly applicable
- Architecture: HIGH — reading actual source code; ExtractText call sites, Poller/Server structs, factory stub all directly verified
- Pitfalls: HIGH (nil guard, temp files, ffmpeg validation) / MEDIUM (whisper-cli `-nt` bug — recommended avoidance confirmed HIGH)
- Test patterns: HIGH — mock Transcriber interface trivial; injectable execCmd follows project's established DI pattern

**Research date:** 2026-03-01
**Valid until:** 2026-04-01 (whisper.cpp CLI flags stable; ffmpeg flags stable)
