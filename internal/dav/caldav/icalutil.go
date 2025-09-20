package caldav

import (
	"bytes"
	"time"
)

// ensureDTStamp inserts a DTSTAMP:YYYYMMDDTHHMMSSZ into the first VEVENT
// if it's missing. Returns possibly modified data and a boolean indicating
// whether an insertion occurred.
func ensureDTStamp(data []byte) ([]byte, bool) {
	up := bytes.ToUpper(data)

	// Quick checks for VCALENDAR and VEVENT envelope
	if !bytes.Contains(up, []byte("BEGIN:VCALENDAR")) || !bytes.Contains(up, []byte("BEGIN:VEVENT")) {
		return data, false
	}

	// Find first VEVENT block
	start := bytes.Index(up, []byte("BEGIN:VEVENT"))
	if start < 0 {
		return data, false
	}
	end := bytes.Index(up[start:], []byte("END:VEVENT"))
	if end < 0 {
		return data, false
	}
	end += start

	block := data[start:end] // VEVENT content region

	// Already has DTSTAMP?
	if bytes.Contains(bytes.ToUpper(block), []byte("DTSTAMP:")) {
		return data, false
	}

	// Build DTSTAMP line
	now := time.Now().UTC().Format("20060102T150405Z")
	dtLine := []byte("\r\nDTSTAMP:" + now)

	// Try to inject right after UID line if present; otherwise right after BEGIN:VEVENT
	injectPos := start
	uidPos := bytes.Index(bytes.ToUpper(block), []byte("\nUID:"))
	if uidPos < 0 {
		// Try CRLF UID
		uidPos = bytes.Index(bytes.ToUpper(block), []byte("\r\nUID:"))
	}
	if uidPos >= 0 {
		// Find end of UID line
		absUIDStart := start + uidPos
		// Line termination: try CRLF then LF
		uidEnd := bytes.Index(data[absUIDStart:], []byte("\r\n"))
		if uidEnd < 0 {
			uidEnd = bytes.Index(data[absUIDStart:], []byte("\n"))
			if uidEnd < 0 {
				uidEnd = 0
			}
		}
		injectPos = absUIDStart + uidEnd
	} else {
		// Inject after BEGIN:VEVENT line end
		afterBegin := bytes.Index(data[start:], []byte("\n"))
		if afterBegin < 0 {
			afterBegin = 0
		}
		injectPos = start + afterBegin
	}

	// Respect existing newline style: choose CRLF if present anywhere, else LF
	nl := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		nl = "\r\n"
	}
	dtLine = []byte(nl + "DTSTAMP:" + now)

	out := make([]byte, 0, len(data)+len(dtLine))
	out = append(out, data[:injectPos]...)
	out = append(out, dtLine...)
	out = append(out, data[injectPos:]...)
	return out, true
}
