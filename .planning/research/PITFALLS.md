# Pitfalls Research

**Domain:** Voice transcription integration for Go WhatsApp bridge (Kapso/OpenClaw)
**Researched:** 2026-03-01
**Confidence:** HIGH (most pitfalls verified with official docs and first-hand community issues)

---

## Critical Pitfalls

### Pitfall 1: Media URL Expiry — 5-Minute Window

**What goes wrong:**
The Kapso/WhatsApp Cloud API media endpoint returns a signed download URL that expires in **5 minutes**. If the download step is deferred, retried lazily, or delayed by a slow queue, the download request returns a 404 or auth error. The media is gone — transcription is impossible without re-requesting the media ID (which may also no longer be valid after extended time).

**Why it happens:**
Developers call `GetMediaURL()` to get the signed URL, then pass the URL forward through queues or channels without immediately downloading the binary. The URL feels like a stable pointer, but it is a time-bomb. The existing `formatMediaMessage()` in `extract.go` already embeds this URL in the event text — the transcript pipeline must download immediately at the same call site where the URL is retrieved.

**How to avoid:**
Download the raw audio bytes immediately after calling `GetMediaURL()` — in the same function, before returning. Never store or pass a signed media URL for later use. The download must complete within the 5-minute window. Add a download deadline via `context.WithTimeout` of at most 4 minutes to leave margin.

```go
// Correct: download immediately in the same call path
media, err := client.GetMediaURL(mediaID)
// ...
audioBytes, err := client.DownloadMedia(ctx, media.URL) // do this NOW
```

**Warning signs:**
- Intermittent transcription failures that correlate with high polling latency or queue depth
- HTTP 404 errors from media download that are not reproducible in tests
- Error messages containing "media expired" or unexplained 401/403 on media URLs

**Phase to address:**
Phase implementing `DownloadMedia()` on the Kapso client — enforce download-on-retrieval as a design constraint, not an afterthought.

---

### Pitfall 2: OGG/Opus MIME Type Mismatch Breaks API Transcription

**What goes wrong:**
WhatsApp voice messages arrive as OGG container files with Opus codec. The `AudioContent.MimeType` field from the webhook may contain `audio/ogg; codecs=opus`, `audio/ogg`, `audio/opus`, or occasionally a bare `application/octet-stream`. Passing the raw MIME string directly to STT API multipart form uploads causes silent failures or rejected requests:

- OpenAI Whisper API rejects bare `.opus` files (not in OGG container) — the file extension in the multipart `Content-Disposition` determines accepted format more than the MIME header
- Groq Whisper API accepts `ogg`, `mp3`, `mp4`, `mpeg`, `mpga`, `m4a`, `wav`, `webm` — `.opus` extension may be rejected
- Setting `Content-Type: audio/opus` (without OGG container specification) produces WhatsApp Cloud API error 131053

Additionally, Go's `multipart.Writer.CreateFormFile()` hardcodes `Content-Type: application/octet-stream` — which causes some API providers to reject the file or fail silently.

**Why it happens:**
Developers assume the MIME type from the webhook is ready to pass through. It is not. The MIME type needs to be normalised, and the multipart form part must be constructed with `textproto.MIMEHeader` + `writer.CreatePart()` rather than `writer.CreateFormFile()` to control the Content-Type explicitly.

**How to avoid:**
1. Normalise the incoming MIME type: map `audio/ogg; codecs=opus`, `audio/ogg`, and `audio/opus` all to `audio/ogg` for HTTP API providers.
2. Use `writer.CreatePart()` with an explicit `textproto.MIMEHeader` — never `CreateFormFile()`.
3. Set the filename in `Content-Disposition` to `audio.ogg` (not `audio.opus`) for OpenAI/Groq compatibility.
4. For whisper.cpp local provider, always convert via `ffmpeg -ar 16000 -ac 1 -c:a pcm_s16le` to a 16-bit mono 16kHz WAV — whisper.cpp CLI requires this exact format; it does NOT accept raw OGG/Opus without the `WHISPER_FFMPEG` build flag.

**Warning signs:**
- HTTP 400 or "unsupported format" errors from STT API that don't reproduce with test WAV files
- Silent empty transcription strings returned from the API with 200 status
- whisper.cpp subprocess exiting with garbled or zero-length output

**Phase to address:**
Phase implementing each provider's `Transcribe()` method — build MIME normalisation as a shared helper used by all providers.

---

### Pitfall 3: Blocking the Message Pipeline During Transcription

**What goes wrong:**
The polling loop and webhook handler in the existing pipeline are synchronous: receive message → `ExtractText()` → forward to gateway. If the transcription call (download + STT API round-trip, or ffmpeg subprocess) is inserted synchronously into this path, every voice message stalls the entire pipeline. During an STT API timeout (which can be 30–60 seconds), no other messages from any user are processed.

**Why it happens:**
The path of least resistance is to call `Transcribe()` directly inside `ExtractText()` or the event loop. The existing `formatMediaMessage()` already does a synchronous `GetMediaURL()` call which is already technically blocking — adding a second network call (download) plus a third (STT API) compounds the problem from milliseconds to tens of seconds.

**How to avoid:**
Run transcription with a per-message context that has a tight deadline (e.g., 30 seconds total for download + transcription). If the context expires, fall back immediately to `[audio] (mime)` without blocking the pipeline. The fallback is already specified in the project requirements — enforce it via `context.WithTimeout` at the call site, not via a separate goroutine that could leak.

The existing pipeline's single-goroutine structure means the simplest correct approach is: call transcription synchronously but always with a hard deadline context. Avoid spawning untracked goroutines for transcription.

**Warning signs:**
- Text messages from other users experience latency spikes when any user sends a voice message
- Goroutine count (`runtime.NumGoroutine()`) grows with audio message volume
- Pipeline throughput drops to near-zero during an STT API degradation event

**Phase to address:**
Phase implementing the extract integration in `delivery/` — bake the context deadline into the `Transcribe()` interface contract, not as a caller responsibility.

---

### Pitfall 4: Temp File Leaks from whisper.cpp Subprocess

**What goes wrong:**
The local whisper.cpp provider requires a two-step temp file flow: download audio → write to `/tmp/` WAV → exec whisper binary → read stdout → delete temp file. If any error occurs between write and delete, the temp file persists. Under continuous use (voice-heavy users), the `/tmp` partition fills up. This is especially dangerous in container or NixOS environments with memory-backed `/tmp` (tmpfs).

A secondary leak vector: if `cmd.Wait()` blocks indefinitely (known Go `os/exec` issue when child subprocesses hold open pipe descriptors), the goroutine executing the subprocess call never returns, and the `defer os.Remove()` never runs.

**Why it happens:**
`defer os.Remove(f.Name())` only runs when the enclosing function returns. If `cmd.Wait()` blocks forever (due to the subprocess holding open a pipe), the defer is never reached. Signal/crash termination also bypasses defers entirely.

**How to avoid:**
1. Always wrap the subprocess call with `context.WithTimeout` — pass `ctx` to `exec.CommandContext()` so Go kills the subprocess on timeout.
2. Use `defer func() { f.Close(); os.Remove(f.Name()) }()` in a closure immediately after `os.CreateTemp()`, before any error paths.
3. Avoid `cmd.Stdout = &bytes.Buffer{}` with pipes — capture stdout via `cmd.Output()` or `cmd.CombinedOutput()` which handles pipe lifecycle correctly.
4. Consider writing temp files to a dedicated subdirectory (`os.MkdirTemp`) that can be nuked on startup as a safety net.

**Warning signs:**
- Growing number of `*.wav` files in `/tmp` visible via `ls /tmp | wc -l`
- whisper.cpp calls that never return (goroutine count accumulation)
- Disk/memory pressure in production that correlates with audio message volume

**Phase to address:**
Phase implementing the local whisper.cpp provider — enforce `exec.CommandContext()` with a timeout from day one, never `exec.Command()`.

---

### Pitfall 5: Error Masking via Silent Fallback

**What goes wrong:**
The project correctly specifies graceful degradation: transcription failure → `[audio] (mime)` fallback. But if the error is swallowed entirely (only logged at DEBUG level or not at all), a systematic failure goes unnoticed for hours. Examples: wrong API key, exhausted Groq rate limits, broken ffmpeg path, media URL always 404 — all degrade silently to `[audio]` without any operational signal that transcription is broken.

**Why it happens:**
The "graceful" requirement is implemented as "always return success with a fallback string" which suppresses the error. The same pattern exists in the current `formatMediaMessage()` — `GetMediaURL()` errors are logged but the function continues happily. Under voice transcription, the stakes are higher because the user's message content is lost rather than merely unlinked.

**How to avoid:**
1. Always log transcription failures at `log.Printf("WARN: transcription failed for %s (%s): %v", messageID, mimeType, err)` — never silently swallow.
2. Track a consecutive-failure counter in the `Transcriber` implementation. After N consecutive failures (e.g., 3), emit a `log.Printf("ERROR: transcription provider appears broken — %d consecutive failures")`. This surfaces systematic failure without breaking the pipeline.
3. In tests, assert that errors are propagated to the caller, not swallowed inside `Transcribe()`.

**Warning signs:**
- All audio messages in logs show `[audio]` fallback with no accompanying WARN lines
- Users report that voice messages "do nothing" but no errors are surfaced
- Test coverage: if `Transcribe()` tests never assert on error propagation, this pitfall is likely present

**Phase to address:**
Phase implementing the `Transcriber` interface — define in the interface contract that errors must be returned (not wrapped into return values), and test error propagation explicitly.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| Using `writer.CreateFormFile()` instead of `CreatePart()` for multipart upload | Simpler code, fewer imports | Hardcodes `application/octet-stream` — some providers reject silently | Never — use `CreatePart()` from the start |
| Sharing a single `http.Client` with no per-request timeout for STT calls | Reuses connection pool | One hung STT request blocks forever; timeouts on the shared client affect all requests | Never for STT calls — use `context.WithTimeout` per request |
| Passing MIME type from webhook directly to STT API without normalisation | Saves a switch statement | OGG/Opus MIME variants cause intermittent API rejections that are hard to reproduce | Never — normalise at ingestion |
| Storing signed media URL in the event struct for later download | Cleaner separation of concerns | 5-minute expiry causes production failures under any load | Never — download immediately |
| `exec.Command()` without context timeout for whisper.cpp | Simpler subprocess call | Subprocess hangs indefinitely; temp files leak; goroutine never returns | Never in production |
| Logging errors at DEBUG level for transcription fallbacks | Cleaner logs in development | Systematic provider failures go unnoticed for hours in production | Only acceptable in tests with assert-on-error |

---

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|----------------|------------------|
| OpenAI Whisper API | Using `CreateFormFile()` which sets `application/octet-stream` | Use `CreatePart()` with explicit `Content-Type: audio/ogg` and filename `audio.ogg` |
| OpenAI Whisper API | Sending raw `.opus` file without OGG container | Rename/treat as `.ogg`; both OGG and OGG/Opus are accepted, raw `.opus` is not |
| Groq Whisper API | Assuming same rate limits as OpenAI | Groq free tier has separate audio-seconds-per-minute limits; check live at `console.groq.com/settings/limits` |
| Groq Whisper API | Not handling 10-second minimum audio billing | Sub-10-second clips are billed as 10 seconds; plan for this in cost modelling |
| Deepgram Nova API | Using default `http.Client` with no timeout | Deepgram pre-recorded API can take up to 10 minutes for large files; set explicit context deadline |
| Deepgram Nova API | Sending binary audio body without `Content-Type` header | Must set `Content-Type` to the correct MIME type in the request body; Deepgram uses binary body + query params, not multipart |
| whisper.cpp subprocess | Using `exec.Command()` with `cmd.Stdout = &bytes.Buffer{}` | Use `exec.CommandContext(ctx, ...)` + `cmd.Output()` which handles pipe lifecycle correctly |
| whisper.cpp subprocess | Passing OGG file directly to whisper CLI | whisper.cpp CLI requires 16-bit mono 16kHz WAV unless built with `WHISPER_FFMPEG` flag; always run `ffmpeg` conversion first |
| Kapso media download | Storing signed URL for later use | Signed URL expires in 5 minutes; download binary immediately, store bytes not URL |
| Kapso media download | Not enforcing size limit before buffering | 25MB limit must be enforced during streaming download, not after reading entire body into memory |

---

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|----------------|
| Buffering entire audio file into `[]byte` before sending to STT API | Works fine for short clips; 10x memory spike for 2-min recordings | Stream from download directly into the STT request body using `io.Pipe()` or pass reader | At a few concurrent voice messages with 25MB files |
| Synchronous STT API call inside polling loop | Voice messages add 2–30s latency to all subsequent messages from all users | Use context deadline (30s max) + immediate fallback; no message should block the queue past its deadline | First time any user sends a voice message during STT API degradation |
| No connection reuse for STT HTTP client | New TLS handshake per transcription call adds 200–500ms latency | Inject a shared `*http.Client` via the `Transcriber` struct (already project pattern) | At any non-trivial volume — TLS overhead is constant per call |
| ffmpeg subprocess spawned per message without concurrency limit | Works for 1-2 concurrent voice messages | Limit concurrent whisper.cpp execs (e.g., `chan struct{}` semaphore); local provider is CPU-bound | When multiple users send voice messages simultaneously — CPU saturation |

---

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| Logging transcribed text content at INFO level | Private voice message contents appear in system logs | Log only metadata (message ID, duration, provider, success/fail) — never log the transcription text itself |
| Not validating audio file size before download | A crafted 1GB URL (or mis-reported `file_size`) exhausts memory/disk | Enforce `max_audio_size` during streaming download via `io.LimitReader`, not after buffering |
| Storing STT API key in config file without file permission check | API key leaks via log aggregators or backup files | Follow existing project pattern: prefer `TRANSCRIBE_API_KEY` env var; note in docs that config file should be `chmod 600` |
| Passing user-controlled `language` config to whisper.cpp CLI as a shell argument without sanitisation | Command injection if language value is not validated | Validate language code against an allowlist of ISO 639-1 codes before passing to `exec.CommandContext` args |

---

## UX Pitfalls

| Pitfall | User Impact | Better Approach |
|---------|-------------|-----------------|
| Sending `[audio] (audio/ogg; codecs=opus)` as fallback text | Users see opaque MIME string; OpenClaw agent cannot act on it | Strip codec parameter from fallback: `[audio] (audio/ogg)` or `[voice message]` |
| No indication in transcribed text that content came from voice | Agent cannot distinguish spoken vs. typed context | Preserve the `[voice] ` prefix as specified — allows agent to adjust response style |
| Transcription of background noise returning garbage text | Agent responds to nonsense, confusing user | Detect suspiciously short transcriptions (< 3 words) from long audio — fall back to `[audio]` rather than forwarding |
| Forwarding transcription errors as message text | "Error: rate limit exceeded" appears in chat as the user's "message" | Never let error strings reach `delivery.Event.Text` — always use the defined fallback format |

---

## "Looks Done But Isn't" Checklist

- [ ] **MIME normalisation:** `audio/ogg; codecs=opus`, `audio/ogg`, and `audio/opus` all route to the same code path — verify with a unit test for each variant
- [ ] **Temp file cleanup:** run the whisper.cpp provider with a cancelled context and verify no `.wav` files remain in `/tmp` after the call
- [ ] **Media URL download deadline:** verify that a deliberately slow media server causes the pipeline to fall back within 30 seconds, not hang indefinitely
- [ ] **Fallback produces log output:** verify that a failed transcription emits at least one `WARN`-level log line — not just a silent `[audio]` return
- [ ] **Size limit enforcement:** verify that a download of a 26MB file is rejected before fully buffering, not after
- [ ] **Concurrent safety:** verify that two simultaneous `Transcribe()` calls on the same provider struct do not race (no shared mutable state — checked with `-race` flag in `just test`)
- [ ] **Config disabled path:** verify that with no `[transcribe]` section in config, audio messages reach OpenClaw as `[audio] (mime)` identical to v0.1.0 behaviour — zero behaviour change

---

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| Media URL expired before download | LOW | Message is already in fallback state (`[audio]`); no user data loss — just notify user to resend if needed |
| Temp files accumulated in `/tmp` | LOW | Add a startup cleanup pass: `filepath.Glob("/tmp/kapso-audio-*.wav")` → remove all on daemon start |
| STT API key exhausted or rate-limited | MEDIUM | Switch provider via config (`TRANSCRIBE_PROVIDER` env var) without restart if using env override; or set provider to empty string to disable transcription |
| whisper.cpp binary path wrong or broken | LOW | Config validation at startup: if provider is `local`, verify binary path exists and is executable before accepting connections |
| Systematic silent fallback (all audio = `[audio]`) | MEDIUM | Add a log query: grep for `[audio]` events vs. `[voice]` events in ratio over time; if ratio flips, provider is broken |

---

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|------------------|--------------|
| Media URL expiry (5-minute window) | Kapso `DownloadMedia()` implementation | Integration test: mock a URL that returns 404 after delay; verify fallback within deadline |
| OGG/Opus MIME type mismatch | Provider `Transcribe()` implementations | Table-driven tests with each MIME variant; test both OpenAI and Groq paths |
| Pipeline blocking | Extract integration in `delivery/` | Benchmark: pipeline throughput with mocked 30s STT delay must not drop below baseline |
| Temp file leaks (whisper.cpp) | Local provider implementation | Test: cancel context mid-exec; assert `/tmp` has no residual files |
| Error masking / silent fallback | Transcriber interface + all provider impls | Test: provider returns error; assert error is logged AND caller receives error value |
| Large file memory pressure | `DownloadMedia()` with size limit | Test: mock server returns 30MB body; assert error returned before full buffer |
| Security — log leakage of transcription text | All provider implementations | Code review gate: grep for `log.*transcript` or `log.*text` in provider files |
| MIME/shell injection in local provider | Local provider implementation | Test: language config with shell metacharacters; assert no command execution side effects |

---

## Sources

- [WhatsApp Cloud API media URL expiry (5 minutes) — Vonage API Support](https://api.support.vonage.com/hc/en-us/articles/4408701311380-How-long-is-media-stored-for-incoming-WhatsApp-messages)
- [WhatsApp Cloud API OGG/Opus MIME type issue — chatwoot/chatwoot #12713](https://github.com/chatwoot/chatwoot/issues/12713)
- [OpenAI Whisper API format support — community discussion on .opus format](https://community.openai.com/t/support-for-opus-file-format/1127125)
- [OpenAI Whisper API audio formats — openai/whisper discussion #799](https://github.com/openai/whisper/discussions/799)
- [whisper.cpp input requirements — PCM 16-bit mono 16kHz](https://github.com/ggml-org/whisper.cpp)
- [whisper.cpp OGG/format support discussion #1399](https://github.com/ggml-org/whisper.cpp/discussions/1399)
- [Go os/exec Wait() blocking issue — golang/go #23019](https://github.com/golang/go/issues/23019)
- [Go goroutine leak prevention — context cancellation patterns](https://dev.to/serifcolakel/go-concurrency-mastery-preventing-goroutine-leaks-with-context-timeout-cancellation-best-1lg0)
- [Groq Whisper API rate limits — GroqDocs](https://console.groq.com/docs/rate-limits)
- [Groq Speech-to-Text supported formats and 10-second minimum](https://console.groq.com/docs/speech-to-text)
- [Deepgram maximum processing time (10 min) — discussion #585](https://github.com/orgs/deepgram/discussions/585)
- [Deepgram pre-recorded audio getting started](https://developers.deepgram.com/docs/pre-recorded-audio)
- [Go multipart CreateFormFile hardcodes application/octet-stream — use CreatePart instead](https://pkg.go.dev/mime/multipart#Writer.CreateFormFile)
- [WhatsApp voice notes broken on model providers — openclaw/openclaw #13924](https://github.com/openclaw/openclaw/issues/13924)

---
*Pitfalls research for: Voice transcription integration — Go WhatsApp bridge (Kapso/OpenClaw)*
*Researched: 2026-03-01*
