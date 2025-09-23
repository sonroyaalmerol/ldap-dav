package common

import (
	"strconv"
	"strings"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/pkg/ical"
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

func MergeIntervalsFB(in []ical.Interval) []ical.Interval {
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
	out := []ical.Interval{in[0]}
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

func SafeCollectionName(s string) bool {
	for _, r := range s {
		// ASCII control chars (0x00-0x1F) and DEL (0x7F) are not allowed
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	if strings.ContainsAny(s, "/") {
		return false
	}
	return true
}

func StrPtr(s string) *string { return &s }
func IntPtr(i int) *int       { return &i }

func ContainsComponent(comps []string, target string) bool {
	for _, comp := range comps {
		if strings.ToUpper(comp) == strings.ToUpper(target) {
			return true
		}
	}
	return false
}

func ExtractTimeRange(f CalendarFilter) *TimeRange {
	c := &f.CompFilter
	for c != nil {
		if c.TimeRange != nil {
			return c.TimeRange
		}
		c = c.CompFilter
	}
	return nil
}

func ExtractComponentFilterNames(f CalendarFilter) []string {
	names := []string{}
	c := &f.CompFilter
	for c != nil {
		if c.Name != "" {
			switch strings.ToUpper(c.Name) {
			case "VCALENDAR":
				// skip; descend
			case "VEVENT", "VTODO", "VJOURNAL":
				names = append(names, strings.ToUpper(c.Name))
			}
		}
		c = c.CompFilter
	}
	return names
}

func ParsePropRequest(_ PropContainer) PropRequest {
	// Default to returning calendar-data and etag for compatibility
	return PropRequest{
		GetETag:      true,
		CalendarData: true,
	}
}

func ParseSeqToken(tok string) (int64, bool) {
	tok = strings.TrimSpace(tok)
	if strings.HasPrefix(tok, "seq:") {
		v := strings.TrimPrefix(tok, "seq:")
		if v == "" {
			return 0, false
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

func BuildFreeBusyICS(start, end time.Time, busyIntervals []ical.Interval, prodID string) []byte {
	var buf strings.Builder

	// VCALENDAR header
	buf.WriteString("BEGIN:VCALENDAR\r\n")
	buf.WriteString("VERSION:2.0\r\n")
	buf.WriteString("PRODID:")
	buf.WriteString(prodID)
	buf.WriteString("\r\n")

	// VFREEBUSY component (REQUIRED per RFC 4791 Section 7.10)
	buf.WriteString("BEGIN:VFREEBUSY\r\n")

	// Required properties per RFC 4791
	buf.WriteString("DTSTAMP:")
	buf.WriteString(time.Now().UTC().Format("20060102T150405Z"))
	buf.WriteString("\r\n")

	buf.WriteString("DTSTART:")
	buf.WriteString(start.UTC().Format("20060102T150405Z"))
	buf.WriteString("\r\n")

	buf.WriteString("DTEND:")
	buf.WriteString(end.UTC().Format("20060102T150405Z"))
	buf.WriteString("\r\n")

	// Optional FREEBUSY properties (empty if no busy intervals)
	for _, interval := range busyIntervals {
		buf.WriteString("FREEBUSY;FBTYPE=BUSY:")
		buf.WriteString(interval.S.UTC().Format("20060102T150405Z"))
		buf.WriteString("/")
		buf.WriteString(interval.E.UTC().Format("20060102T150405Z"))
		buf.WriteString("\r\n")
	}

	buf.WriteString("END:VFREEBUSY\r\n")
	buf.WriteString("END:VCALENDAR\r\n")

	return []byte(buf.String())
}
