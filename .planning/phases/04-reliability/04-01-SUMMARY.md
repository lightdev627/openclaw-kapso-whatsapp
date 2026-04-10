---
phase: 04-reliability
plan: 01
subsystem: transcribe
tags: [go, whisper, openai, groq, no_speech_prob, hallucination_guard, debug_logging, retry]

# Dependency graph
requires:
  - phase: 02-providers
    provides: openAIWhisper struct and verbose_json Transcribe() implementation
  - phase: 01-foundation
    provides: TranscribeConfig struct with 3-tier config precedence

provides:
  - TranscribeConfig with Debug, NoSpeechThreshold, CacheTTL fields and env overrides
  - whisperVerboseResponse type parsing segments with avg_logprob and no_speech_prob
  - noSpeechError type for hallucination guard rejections
  - No-speech guard that rejects transcripts when no_speech_prob >= threshold
  - Debug logging via [transcribe:debug] prefix with quality metrics
  - INFR-01 formally verified via retry.go comment

affects:
  - 04-02-cache (uses CacheTTL field)
  - 04-03-delivery (handles noSpeechError as [audio] fallback)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - noSpeechError unexported type for domain-specific rejection distinct from httpError
    - primitive fields on openAIWhisper struct (NoSpeechThreshold, Debug) not config import
    - providerName() derived from BaseURL string match for logging without coupling to config

key-files:
  created: []
  modified:
    - internal/config/config.go
    - internal/transcribe/openai.go
    - internal/transcribe/openai_test.go
    - internal/transcribe/retry.go

key-decisions:
  - "noSpeechError is unexported — delivery layer catches via errors.As, not string match"
  - "Empty segments array skips guard — short silent clips have no segments, rejecting them would be wrong"
  - "providerName() derives from BaseURL containing 'groq' — avoids adding a provider name field for logging only"
  - "Guard uses maxNoSpeech (worst segment) not average — one bad segment indicates noise in clip"
  - "Log WARN before returning noSpeechError — silent fallback breaks observability"

patterns-established:
  - "Primitive injection on struct (NoSpeechThreshold, Debug) — same pattern as Model, Language already on openAIWhisper"
  - "noSpeechError vs httpError — separate domain errors for different rejection reasons"
  - "gofmt -w applied after struct alignment changes — CI fmt-check required"

requirements-completed: [TRNS-04, INFR-01, INFR-04]

# Metrics
duration: 3min
completed: 2026-03-01
---

# Phase 4 Plan 01: Config fields, no_speech_prob hallucination guard, and debug logging Summary

**Whisper verbose_json parsing expanded to capture no_speech_prob per segment, with configurable threshold guard (default 0.85) that returns noSpeechError instead of hallucinated transcript, plus [transcribe:debug] quality metric logging**

## Performance

- **Duration:** 3 min
- **Started:** 2026-03-01T16:26:50Z
- **Completed:** 2026-03-01T16:29:11Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments

- Added Debug, NoSpeechThreshold (default 0.85), CacheTTL (default 3600) fields to TranscribeConfig with TOML tags, defaults, env overrides (KAPSO_TRANSCRIBE_DEBUG/NO_SPEECH_THRESHOLD/CACHE_TTL), and Validate() zero-guards
- Expanded openai.go from anonymous `{Text string}` to full whisperVerboseResponse including segments with avg_logprob and no_speech_prob; defined noSpeechError type for guard rejections
- No-speech guard rejects transcripts when max no_speech_prob across segments >= threshold; empty segments skip the guard entirely to allow short clips through
- Debug mode emits [transcribe:debug] log line with provider (groq/openai from BaseURL), model, language, avg_logprob, no_speech_prob, duration_ms
- INFR-01 formally verified: retry.go confirmed to have 3 attempts, 1s base, 2x factor, 0.25 jitter, context cancellation — no code changes needed

## Task Commits

Each task was committed atomically:

1. **Task 1: Add config fields and env overrides for Phase 4 features** - `2daa31a` (feat)
2. **Task 2: Expand verbose_json parsing, add no_speech_prob guard and debug logging** - `a962afd` (feat, TDD)

**Plan metadata:** (final docs commit — see below)

_Note: Task 2 was a TDD task. Tests written first (RED: build failed on unknown NoSpeechThreshold field and undefined noSpeechError), then implementation written to pass (GREEN: all 11 tests pass)._

## Files Created/Modified

- `internal/config/config.go` - Added NoSpeechThreshold (float64), CacheTTL (int), Debug (bool) to TranscribeConfig; defaults and env overrides; CacheTTL zero-guard in Validate()
- `internal/transcribe/openai.go` - Added noSpeechError type, NoSpeechThreshold/Debug fields to openAIWhisper, whisperVerboseResponse struct, no_speech_prob guard logic, debug logging, providerName() helper
- `internal/transcribe/openai_test.go` - Updated whisperVerboseJSON fixture to include segments; added whisperHighNoSpeechJSON and whisperEmptySegmentsJSON fixtures; added 4 new test cases
- `internal/transcribe/retry.go` - Added INFR-01 verification comment at package level

## Decisions Made

- noSpeechError is unexported — delivery layer catches via errors.As, not string match, keeping error handling type-safe
- Empty segments array skips guard — short silent clips have no segments; rejecting them would incorrectly drop valid short speech
- providerName() derives "groq" or "openai" from BaseURL string — avoids adding a provider name field just for logging
- Guard uses maxNoSpeech (worst segment) not average — one bad segment is enough to indicate significant noise
- Log WARN before returning noSpeechError — silent fallback would break operator observability

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Applied gofmt after struct alignment changes**
- **Found during:** Task 2 (just check fmt-check step)
- **Issue:** Struct field alignment in TranscribeConfig and test struct literal whitespace triggered gofmt lint failure
- **Fix:** Ran `gofmt -w` on affected files (config.go, openai_test.go, and pre-existing test files that were already unformatted)
- **Files modified:** internal/config/config.go, internal/transcribe/openai_test.go, internal/transcribe/deepgram_test.go, internal/transcribe/factory_internal_test.go, internal/transcribe/local_test.go, internal/transcribe/transcribe_test.go
- **Verification:** `just check` passes with no fmt-check errors
- **Committed in:** a962afd (part of Task 2 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - formatting)
**Impact on plan:** Necessary for CI to pass. Pre-existing test files were already unformatted; gofmt touched them as part of bulk fix. No behavior changes.

## Issues Encountered

None — plan executed cleanly. TDD cycle worked as intended: build failure on unknown struct fields confirmed RED phase, then implementation passed all 11 tests on first attempt.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Config foundation ready for Phase 4 Plan 02 (caching) — CacheTTL field available
- noSpeechError defined and ready for Phase 4 Plan 03 (delivery) to catch as [audio] fallback
- INFR-01 closed — retry infrastructure verified without changes needed
- All existing tests pass confirming no regressions to Phase 2/3 work

---
*Phase: 04-reliability*
*Completed: 2026-03-01*
