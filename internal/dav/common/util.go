package common

import (
	"strings"
	"time"
)

func ParseICalTime(s string) (time.Time, error) {
	if len(s) == 8 {
		return time.Parse("20060102", s)
	}
	if strings.HasSuffix(s, "Z") {
		return time.Parse("20060102T150405Z", s)
	}
	return time.Parse(time.RFC3339, s)
}

func TrimQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func MaxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func MinTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

type Interval struct{ S, E time.Time }

func MergeIntervalsFB(in []Interval) []Interval {
	if len(in) <= 1 {
		return in
	}
	// simple insertion sort by start
	for i := 1; i < len(in); i++ {
		j := i
		for j > 0 && in[j-1].S.After(in[j].S) {
			in[j-1], in[j] = in[j], in[j-1]
			j--
		}
	}
	out := []Interval{in[0]}
	for i := 1; i < len(in); i++ {
		last := &out[len(out)-1]
		if in[i].S.After(last.E) {
			out = append(out, in[i])
		} else if in[i].E.After(last.E) {
			last.E = in[i].E
		}
	}
	return out
}

func SafeSegment(s string) bool {
	return s != "" && !strings.Contains(s, "/") && !strings.Contains(s, "\\") && !strings.Contains(s, "..")
}
