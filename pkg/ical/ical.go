package ical

import (
	"bytes"

	"github.com/emersion/go-ical"
)

func NormalizeICS(data []byte) ([]byte, error) {
	// Optionally parse and re-serialize to ensure validity and consistent formatting
	cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := ical.NewEncoder(&buf)
	if err := enc.Encode(cal); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
