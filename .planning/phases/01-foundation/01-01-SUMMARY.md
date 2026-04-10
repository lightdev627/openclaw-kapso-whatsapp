---
phase: 01-foundation
plan: 01
subsystem: config
tags: [go, toml, env-vars, config, transcription]

# Dependency graph
requires: []
provides:
  - TranscribeConfig struct with 8 fields (Provider, APIKey, Model, Language, MaxAudioSize, BinaryPath, ModelPath, Timeout)
  - Config.Transcribe field with toml:"transcribe" TOML tag
  - defaults() sets MaxAudioSize=25MB, BinaryPath=whisper-cli, Timeout=30
  - applyEnv() handles all 7 KAPSO_TRANSCRIBE_* env vars (Provider auto-lowercased)
  - Validate() guard: MaxAudioSize<=0 reset to 25MB
  - Full test coverage via table-driven tests
affects: [02-providers, 03-integration, 04-polish]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "3-tier config: defaults < TOML file < env vars"
    - "Env var provider lowercasing in applyEnv() for case-insensitive input"
    - "Validate() zero-guard pattern for int64 fields that TOML can mask as 0"
    - "TDD: RED (failing test commit) then GREEN (implementation commit)"

key-files:
  created:
    - internal/config/config_test.go
  modified:
    - internal/config/config.go

key-decisions:
  - "MaxAudioSize validated to non-zero in Validate() to guard against TOML decoding zero-value masking defaults"
  - "Provider env var is lowercased in applyEnv() so KAPSO_TRANSCRIBE_PROVIDER=GROQ and groq both work identically"
  - "Empty provider is a valid config state — no provider = transcription disabled, not an error"

patterns-established:
  - "Validate() zero-guard: reset int64 fields to default when <=0 (TOML zero-value masking)"
  - "applyEnv() env var lowercasing for provider/mode fields for case-insensitive user input"

requirements-completed: [CONF-01, CONF-02, CONF-03, CONF-04, CONF-05]

# Metrics
duration: 2min
completed: 2026-03-01
---

# Phase 1 Plan 01: TranscribeConfig Config Section Summary

**TranscribeConfig struct with 8 fields, 3-tier config (defaults/TOML/env), Validate() zero-guard, and full table-driven test coverage for the transcription configuration foundation**

## Performance

- **Duration:** 2 min
- **Started:** 2026-03-01T13:48:21Z
- **Completed:** 2026-03-01T13:49:56Z
- **Tasks:** 1 (TDD: 2 commits — test RED + implementation GREEN)
- **Files modified:** 2

## Accomplishments
- Added `TranscribeConfig` struct with all 8 required fields and correct TOML tags
- Extended `Config` struct with `Transcribe TranscribeConfig \`toml:"transcribe"\`` field
- Implemented defaults: MaxAudioSize=25MB, BinaryPath="whisper-cli", Timeout=30
- Implemented `applyEnv()` overrides for all 7 `KAPSO_TRANSCRIBE_*` env vars with provider lowercasing
- Added `Validate()` guard resetting `MaxAudioSize` to 25MB when zero or negative
- Full test coverage: 12 subtests across 5 test functions, all passing

## Task Commits

Each task was committed atomically (TDD pattern):

1. **Task 1: RED - Failing tests** - `68cf472` (test)
2. **Task 1: GREEN - Implementation** - `926124b` (feat)

_Note: TDD tasks have multiple commits (test then feat)_

## Files Created/Modified
- `internal/config/config.go` - Added TranscribeConfig struct, Config.Transcribe field, defaults, applyEnv overrides, Validate guard
- `internal/config/config_test.go` - Created: 5 test functions with 12 subtests covering defaults, env overrides, TOML parsing, 3-tier precedence, zero-guard, empty provider

## Decisions Made
- Provider env var auto-lowercased in `applyEnv()` so users can set `GROQ`, `Groq`, or `groq` interchangeably
- `Validate()` resets `MaxAudioSize <= 0` to 25MB to guard against TOML zero-value masking (when a TOML file sets `max_audio_size = 0` it would silently override the default without this guard)
- Empty provider is valid — transcription is opt-in and disabled by default; no error when provider is unset

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required. Config keys defined, but no secrets needed at this layer.

## Next Phase Readiness
- `TranscribeConfig` is the foundation for the transcriber factory (Plan 02) and all provider implementations
- Plans 02+ can read `cfg.Transcribe.Provider` to select the appropriate provider
- All 3-tier config mechanics tested and verified

## Self-Check: PASSED

All files verified present. All commits verified in git log.

---
*Phase: 01-foundation*
*Completed: 2026-03-01*
