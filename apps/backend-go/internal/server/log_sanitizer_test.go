package server

import (
	"fmt"
	"testing"
)

// BUG-05 regression: raw tqdm frames like "0%|...530M..." must never reach
// the user-facing Logs screen verbatim.
func TestProgressLineFilterCompactsTqdmFrame(t *testing.T) {
	f := &progressLineFilter{}
	clean, ok := f.sanitize(`42%|##########5555            | 530M/1.20G [00:03<00:05, 150MB/s]`)
	if !ok {
		t.Fatal("expected the first progress frame to be surfaced")
	}
	if clean != "download 42%" {
		t.Fatalf("expected a compact 'download 42%%' line, got %q", clean)
	}
}

// A single scanned line can itself contain many \r-separated tqdm redraws
// (no real newline in between) - only the LAST frame reflects current state.
func TestProgressLineFilterKeepsOnlyLastCarriageReturnSegment(t *testing.T) {
	f := &progressLineFilter{}
	raw := "0%|          | 0/530M\r5%|#         | 26M/530M\r97%|#########9| 514M/530M"
	clean, ok := f.sanitize(raw)
	if !ok {
		t.Fatal("expected a progress line to be surfaced")
	}
	if clean != "download 97%" {
		t.Fatalf("expected only the last frame (97%%), got %q", clean)
	}
}

// Successive small percentage bumps must be throttled so a download does not
// flood the log buffer (which is capped) with dozens of near-duplicate
// lines, evicting genuinely useful earlier entries.
func TestProgressLineFilterThrottlesSmallIncrements(t *testing.T) {
	f := &progressLineFilter{}
	var seen []string
	for _, pct := range []int{0, 1, 2, 5, 9, 10, 11, 15, 20, 55, 100} {
		line := fmt.Sprintf("%d%%|####      | some/bytes", pct)
		if clean, ok := f.sanitize(line); ok {
			seen = append(seen, clean)
		}
	}
	// Only jumps of >=10 percentage points (plus the very first one) should
	// have been surfaced: 0, 10, 20, 55, 100 (not the noise in between).
	want := []string{"download 0%", "download 10%", "download 20%", "download 55%", "download 100%"}
	if len(seen) != len(want) {
		t.Fatalf("expected %v, got %v", want, seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, seen)
		}
	}
}

// Non-progress lines (real errors, normal pipeline events) must always pass
// through unchanged - this filter must never hide a genuine error.
func TestProgressLineFilterPassesThroughNormalLines(t *testing.T) {
	f := &progressLineFilter{}
	clean, ok := f.sanitize("Traceback (most recent call last): connection refused")
	if !ok {
		t.Fatal("expected a normal error line to pass through")
	}
	if clean != "Traceback (most recent call last): connection refused" {
		t.Fatalf("expected the line unchanged, got %q", clean)
	}
}

func TestProgressLineFilterDropsBlankLines(t *testing.T) {
	f := &progressLineFilter{}
	if _, ok := f.sanitize("   "); ok {
		t.Fatal("expected a blank line to be dropped")
	}
	if _, ok := f.sanitize("\r\r\r"); ok {
		t.Fatal("expected a line of only carriage returns to be dropped")
	}
}
