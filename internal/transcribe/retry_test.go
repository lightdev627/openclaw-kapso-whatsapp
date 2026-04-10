package transcribe

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// mockTranscriber returns a pre-defined sequence of (result, error) pairs.
// After exhausting the sequence, it returns the last entry repeatedly.
type mockTranscriber struct {
	results []mockResult
	calls   int
}

type mockResult struct {
	text string
	err  error
}

func (m *mockTranscriber) Transcribe(_ context.Context, _ []byte, _ string) (string, error) {
	i := m.calls
	if i >= len(m.results) {
		i = len(m.results) - 1
	}
	m.calls++
	return m.results[i].text, m.results[i].err
}

func TestRetryTranscriber(t *testing.T) {
	noopSleep := func(time.Duration) {}

	makeHTTPError := func(code int) error {
		return &httpError{StatusCode: code, Body: "test error"}
	}

	tests := []struct {
		name            string
		results         []mockResult
		wantText        string
		wantErr         bool
		wantErrContains string
		wantCalls       int
		cancelCtx       bool // use pre-cancelled context
	}{
		{
			name: "success first try",
			results: []mockResult{
				{text: "hello", err: nil},
			},
			wantText:  "hello",
			wantCalls: 1,
		},
		{
			name: "retry on 429 then success",
			results: []mockResult{
				{err: makeHTTPError(429)},
				{text: "hello", err: nil},
			},
			wantText:  "hello",
			wantCalls: 2,
		},
		{
			name: "retry on 5xx then success",
			results: []mockResult{
				{err: makeHTTPError(503)},
				{text: "hello", err: nil},
			},
			wantText:  "hello",
			wantCalls: 2,
		},
		{
			name: "no retry on 400",
			results: []mockResult{
				{err: makeHTTPError(400)},
			},
			wantErr:         true,
			wantErrContains: "provider returned 400",
			wantCalls:       1,
		},
		{
			name: "no retry on 401",
			results: []mockResult{
				{err: makeHTTPError(401)},
			},
			wantErr:         true,
			wantErrContains: "provider returned 401",
			wantCalls:       1,
		},
		{
			name: "exhausted after 3 attempts",
			results: []mockResult{
				{err: makeHTTPError(429)},
				{err: makeHTTPError(429)},
				{err: makeHTTPError(429)},
			},
			wantErr:         true,
			wantErrContains: "transcribe failed after 3 attempts",
			wantCalls:       3,
		},
		{
			name:            "context cancelled returns immediately",
			results:         []mockResult{{text: "hello", err: nil}},
			wantErr:         true,
			cancelCtx:       true,
			wantErrContains: "context canceled",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockTranscriber{results: tc.results}
			rt := newRetryTranscriber(mock, 0) // 0 timeout = no timeout
			rt.base = 1 * time.Millisecond
			rt.sleepFunc = noopSleep

			ctx := context.Background()
			if tc.cancelCtx {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel() // cancel immediately
			}

			got, err := rt.Transcribe(ctx, []byte("audio"), "audio/ogg")

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.wantErrContains != "" && !strings.Contains(err.Error(), tc.wantErrContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrContains)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tc.wantText {
					t.Errorf("Transcribe() = %q, want %q", got, tc.wantText)
				}
			}

			if tc.wantCalls > 0 && mock.calls != tc.wantCalls {
				t.Errorf("inner called %d times, want %d", mock.calls, tc.wantCalls)
			}
		})
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"non-http error", errors.New("generic error"), false},
		{"429 is retryable", &httpError{StatusCode: 429}, true},
		{"500 is retryable", &httpError{StatusCode: 500}, true},
		{"502 is retryable", &httpError{StatusCode: 502}, true},
		{"503 is retryable", &httpError{StatusCode: 503}, true},
		{"400 is not retryable", &httpError{StatusCode: 400}, false},
		{"401 is not retryable", &httpError{StatusCode: 401}, false},
		{"403 is not retryable", &httpError{StatusCode: 403}, false},
		{"404 is not retryable", &httpError{StatusCode: 404}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isRetryable(tc.err)
			if got != tc.want {
				t.Errorf("isRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
