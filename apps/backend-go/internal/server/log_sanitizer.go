package server

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// tqdmPercentPattern recognizes a tqdm-style progress frame, e.g.
// "42%|########..    | 530M/1.2G [00:03<00:05, 150MB/s]".
var tqdmPercentPattern = regexp.MustCompile(`(\d{1,3})%\|`)

// progressLineFilter compacts noisy child-process progress output (bare
// carriage-return redraws, unicode/ASCII block bars, raw byte counters like
// "530M") into short "download NN%" lines, and throttles them so a single
// bootstrap download does not flood the log buffer with dozens of near-
// duplicate entries (BUG-05: raw tqdm frames like "0%|...530M..." showing up
// verbatim in the user-facing Logs screen). Non-progress lines always pass
// through untouched so real errors are never hidden.
type progressLineFilter struct {
	lastPercent int
	sawPercent  bool
}

// sanitize returns the line to log (if any) and whether it should be
// emitted at all. A raw scanner line can itself contain many \r-separated
// progress frames (tqdm redraws in place without newlines) - only the last
// frame reflects the current state, so earlier ones in the same line are
// discarded before throttling.
func (f *progressLineFilter) sanitize(raw string) (string, bool) {
	segments := strings.Split(raw, "\r")
	last := strings.TrimSpace(segments[len(segments)-1])
	if last == "" {
		return "", false
	}

	match := tqdmPercentPattern.FindStringSubmatch(last)
	if match == nil {
		return last, true
	}

	percent, err := strconv.Atoi(match[1])
	if err != nil {
		return last, true
	}
	if f.sawPercent && percent < 100 && percent-f.lastPercent < 10 {
		return "", false
	}
	f.lastPercent = percent
	f.sawPercent = true
	return fmt.Sprintf("download %d%%", percent), true
}
