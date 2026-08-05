package server

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestClassifyResumeError(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		code   string
		status int
	}{
		{"missing name (wrapped)", fmt.Errorf("invalid extracted resume: %w", errMissingName), "missing_name", 422},
		{"invalid ai json (wrapped)", fmt.Errorf("%w: unexpected token", errInvalidAIJSON), "invalid_ai_json", 502},
		{"context timeout", fmt.Errorf("resume parse: %w", context.DeadlineExceeded), "ai_timeout", 504},
		{"rate limited", errors.New("gemini: HTTP 429: quota exceeded"), "ai_rate_limited", 502},
		{"unauthorized", errors.New("gemini: HTTP 401: invalid key"), "ai_key_invalid", 502},
		{"forbidden", errors.New("gemini: HTTP 403: blocked"), "ai_key_invalid", 502},
		{"service unavailable", errors.New("gemini: HTTP 503: overloaded"), "ai_unavailable", 502},
		{"unknown error", errors.New("connection reset"), "ai_unavailable", 502},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyResumeError(tc.err)
			if got.Code != tc.code {
				t.Fatalf("code: got %q, want %q", got.Code, tc.code)
			}
			if got.Status != tc.status {
				t.Fatalf("status: got %d, want %d", got.Status, tc.status)
			}
			if got.Message == "" {
				t.Fatal("message must not be empty")
			}
		})
	}
}

func TestResumeErrorMessagesAreEnglish(t *testing.T) {
	// Guard-rail RS-UX-04: nenhuma mensagem da taxonomia pode conter acento PT.
	errs := []error{
		fmt.Errorf("x: %w", errMissingName),
		fmt.Errorf("x: %w", errInvalidAIJSON),
		fmt.Errorf("x: %w", context.DeadlineExceeded),
		errors.New("HTTP 429"),
		errors.New("HTTP 401"),
		errors.New("HTTP 503"),
		errors.New("other"),
	}
	for _, err := range errs {
		msg := classifyResumeError(err).Message
		for _, r := range msg {
			if r > 0x7f {
				t.Fatalf("non-ASCII rune %q in message %q (PT text leaking?)", r, msg)
			}
		}
	}
}
