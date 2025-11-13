package ical

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

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

func parseMultipleDates(dateStr string) ([]time.Time, error) {
	var dates []time.Time
	parts := strings.Split(dateStr, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		date, _, err := parseDateTime(part)
		if err != nil {
			continue
		}
		dates = append(dates, date)
	}

	return dates, nil
}

func parseDuration(durStr string) (time.Duration, error) {
	durStr = strings.TrimSpace(durStr)
	if !strings.HasPrefix(durStr, "P") {
		return 0, fmt.Errorf("invalid duration format")
	}

	var days, hours, minutes, seconds int
	var inTime bool
	var current strings.Builder

	for _, r := range durStr[1:] {
		switch r {
		case 'D':
			if n, err := strconv.Atoi(current.String()); err == nil {
				days = n
			}
			current.Reset()
		case 'T':
			inTime = true
			current.Reset()
		case 'H':
			if inTime {
				if n, err := strconv.Atoi(current.String()); err == nil {
					hours = n
				}
			}
			current.Reset()
		case 'M':
			if inTime {
				if n, err := strconv.Atoi(current.String()); err == nil {
					minutes = n
				}
			}
			current.Reset()
		case 'S':
			if inTime {
				if n, err := strconv.Atoi(current.String()); err == nil {
					seconds = n
				}
			}
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}

	return time.Duration(days)*24*time.Hour +
		time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds)*time.Second, nil
}

func filterExcludedDates(instances, exdates []time.Time) []time.Time {
	if len(exdates) == 0 {
		return instances
	}

	excludeMap := make(map[string]bool)
	for _, exdate := range exdates {
		excludeMap[exdate.Format("20060102T150405Z")] = true
	}

	var filtered []time.Time
	for _, instance := range instances {
		key := instance.Format("20060102T150405Z")
		if !excludeMap[key] {
			filtered = append(filtered, instance)
		}
	}

	return filtered
}
