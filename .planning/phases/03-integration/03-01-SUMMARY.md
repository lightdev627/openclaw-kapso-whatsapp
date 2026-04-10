---
phase: 03-integration
plan: "01"
subsystem: transcription
tags: [whisper, ffmpeg, subprocess, local, tdd, dependency-injection]

requires:
  - phase: 02-cloud-providers
    provides: Transcriber interface, factory function (transcribe.New), retry wrapper pattern

provides:
  - localWhisper struct implementing Transcriber via ffmpeg + whisper-cli subprocesses
  - newLocalWhisper constructor with ModelPath validation at construction time
  - Factory "local" case with dual startup validation (whisper-cli + ffmpeg in PATH)
  - Injectable execCmd for subprocess mocking in tests

affects: [03-integration, 04-relay]

tech-stack:
  added: []
  patterns:
    - "MkdirTemp + defer RemoveAll for process temp file isolation"
    - "execCmd function field for subprocess injection (same pattern as mockable now() in Phase 1)"
    - "CombinedOutput() for subprocess error capture (not Stdout/Stderr split)"
    - "Return early from factory for local case — no retry wrapper (local failures are not transient)"

key-files:
  created:
    - internal/transcribe/local.go
    - internal/transcribe/local_test.go
  modified:
    - internal/transcribe/transcribe.go

key-decisions:
  - "No retry wrapper on local provider — local subprocess failures (model not found, audio corruption) are not transient and retrying would waste CPU"
  - "MkdirTemp + RemoveAll over individual CreateTemp — single cleanup call, no orphaned files if process crashes mid-execution"
  - "execCmd field on struct (same pattern as mockable now()) — allows test to intercept both ffmpeg and whisper-cli calls without real binaries"
  - "ffmpeg validated in factory at startup (LookPath) not in newLocalWhisper — keeps struct constructor focused on config validation"
  - "outputPrefix + .txt file path (not stdout) — whisper-cli -otxt flag writes to file; reading stdout would require different flags and risk buffering issues"

patterns-established:
  - "Subprocess injection: func(ctx context.Context, name string, args ...string) *exec.Cmd field, defaults to exec.CommandContext"
  - "Test mock dispatches on binary name: ffmpeg side-effect writes WAV, whisper-cli side-effect writes txt"
  - "Temp dir captured from args in cleanup tests via string path search"

requirements-completed: [LOCL-01, LOCL-02, LOCL-03, LOCL-04, TEST-02]

duration: 3min
completed: 2026-03-01
---

# Phase 3 Plan 01: Local Whisper Provider Summary

**localWhisper provider using ffmpeg OGG-to-WAV conversion and whisper-cli subprocess with injectable execCmd, temp dir cleanup, and dual startup validation in factory**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-03-01T15:57:31Z
- **Completed:** 2026-03-01T15:59:42Z
- **Tasks:** 2 (TDD: RED + GREEN + factory wire)
- **Files modified:** 3

## Accomplishments

- Implemented `localWhisper` struct with injectable `execCmd` field for subprocess mocking without real binaries
- Full temp directory isolation using `os.MkdirTemp` + `defer os.RemoveAll` — cleanup guaranteed on all paths including context cancellation
- Factory "local" case now validates both `whisper-cli` and `ffmpeg` in PATH at startup, then constructs `localWhisper` directly (no retry wrapper)
- Comprehensive table-driven test suite: 6 behavior tests + 2 cleanup tests covering success, ffmpeg failure, whisper-cli failure, language flag inclusion/exclusion, and temp dir cleanup

## Task Commits

Each task was committed atomically:

1. **TDD RED — Failing tests for localWhisper** - `28876a3` (test)
2. **TDD GREEN — localWhisper implementation** - `0806ced` (feat)
3. **Task 2: Wire local provider into factory** - `6342d9a` (feat)

## Files Created/Modified

- `internal/transcribe/local.go` - localWhisper struct, newLocalWhisper constructor, Transcribe method with ffmpeg+whisper-cli subprocess execution
- `internal/transcribe/local_test.go` - Table-driven tests with injectable execCmd mock; cleanup verification tests
- `internal/transcribe/transcribe.go` - Factory "local" case: added ffmpeg LookPath check, replaced stub error with newLocalWhisper(cfg)

## Decisions Made

- **No retry wrapper** on local provider — local subprocess failures (model not found, audio corruption) are not transient; retrying wastes CPU
- **`MkdirTemp` + `RemoveAll`** over individual `CreateTemp` — single cleanup call handles all intermediate files atomically
- **`execCmd` function field** (same injectable pattern as `now()` in Phase 1) — lets tests intercept both ffmpeg and whisper-cli without real binaries
- **ffmpeg validated in factory** (not in `newLocalWhisper`) — struct constructor focuses on config-level validation; binary availability is an environment concern
- **outputPrefix + .txt** via `-otxt` flag — whisper-cli writes transcript to file; reading stdout would require different flags and risks buffering

## Deviations from Plan

None - plan executed exactly as written.

Note: Pre-existing build failures exist in `internal/delivery/poller` and `internal/delivery/webhook` (calling `delivery.ExtractText` with old signature — future plan 03-02 has partially applied changes). These are out of scope for 03-01. The `internal/transcribe` package builds and vets cleanly.

## Issues Encountered

Pre-existing `go build ./cmd/...` failures in delivery package (03-02 signature changes partially applied to working tree but not committed). Not caused by 03-01 changes. Transcribe package itself builds and vets cleanly.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- `localWhisper` implements `Transcriber` — ready for use in `delivery.ExtractText` audio branch (Plan 03-02)
- Factory "local" case fully wired — operators can configure `provider = "local"` with `binary_path` and `model_path`
- All transcribe package tests pass (11/11 test functions, 30+ subtests)

---
*Phase: 03-integration*
*Completed: 2026-03-01*
