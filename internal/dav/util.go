package dav

import (
	"strings"
	"time"
)

func parseICalTime(s string) (time.Time, error) {
	if len(s) == 8 {
		return time.Parse("20060102", s)
	}
	if strings.HasSuffix(s, "Z") {
		return time.Parse("20060102T150405Z", s)
	}
	return time.Parse(time.RFC3339, s)
}

func trimQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

type interval struct{ s, e time.Time }

func mergeIntervalsFB(in []interval) []interval {
	if len(in) <= 1 {
		return in
	}
	// simple insertion sort by start
	for i := 1; i < len(in); i++ {
		j := i
		for j > 0 && in[j-1].s.After(in[j].s) {
			in[j-1], in[j] = in[j], in[j-1]
			j--
		}
	}
	out := []interval{in[0]}
	for i := 1; i < len(in); i++ {
		last := &out[len(out)-1]
		if in[i].s.After(last.e) {
			out = append(out, in[i])
		} else if in[i].e.After(last.e) {
			last.e = in[i].e
		}
	}
	return out
}

func safeSegment(s string) bool {
	return s != "" && !strings.Contains(s, "/") && !strings.Contains(s, "\\") && !strings.Contains(s, "..")
}
