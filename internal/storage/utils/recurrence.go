package utils

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

func ExtractRecurrenceIDFromData(data string) (*time.Time, error) {
	if !strings.Contains(data, "RECURRENCE-ID") {
		return nil, fmt.Errorf("no RECURRENCE-ID found")
	}

	// Parse to get the actual value
	cal, err := ical.NewDecoder(bytes.NewReader([]byte(data))).Decode()
	if err != nil {
		return nil, err
	}

	for _, comp := range cal.Children {
		if comp.Name != ical.CompEvent {
			continue
		}

		if recID := comp.Props.Get(ical.PropRecurrenceID); recID != nil {
			t, _, err := parseDateTime(recID.Value)
			if err != nil {
				return nil, err
			}
			return &t, nil
		}
	}

	return nil, fmt.Errorf("no RECURRENCE-ID found")
}

func ExtractRecurrenceIDValue(data string) string {
	// Extract just the value part of RECURRENCE-ID for LIKE matching
	// Example: RECURRENCE-ID:20250115T100000Z -> 20250115T100000Z
	lines := strings.Split(data, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "RECURRENCE-ID") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func parseDateTime(s string) (time.Time, bool, error) {
	s = strings.TrimSpace(s)

	if len(s) == 8 {
		t, err := time.Parse("20060102", s)
		return t, true, err
	}

	if len(s) == 15 {
		t, err := time.ParseInLocation("20060102T150405", s, time.Local)
		return t, false, err
	}
	if len(s) == 16 && strings.HasSuffix(s, "Z") {
		t, err := time.Parse("20060102T150405Z", s)
		return t, false, err
	}

	t, err := time.Parse(time.RFC3339, s)
	return t, false, err
}
