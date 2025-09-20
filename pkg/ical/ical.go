package ical

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/emersion/go-ical"
)

type Interval struct{ S, E time.Time }

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

func DetectICSComponent(data []byte) (string, error) {
	dec := ical.NewDecoder(bytes.NewReader(data))
	cal, err := dec.Decode()
	if err != nil {
		return "", err
	}

	// Get first component of supported type
	for _, child := range cal.Children {
		if child.Name == ical.CompEvent ||
			child.Name == ical.CompToDo ||
			child.Name == ical.CompJournal {
			return child.Name, nil
		}
	}

	return "", errors.New("unsupported component")
}

func EnsureDTStamp(data []byte) ([]byte, bool) {
	dec := ical.NewDecoder(bytes.NewReader(data))
	cal, err := dec.Decode()
	if err != nil {
		return data, false
	}

	modified := false

	// Process all components in the calendar
	for _, child := range cal.Children {
		if child.Name == ical.CompEvent {
			// Check if DTSTAMP already exists
			if child.Props.Get(ical.PropDateTimeStamp) == nil {
				// Add DTSTAMP property
				now := time.Now().UTC()
				prop := ical.NewProp(ical.PropDateTimeStamp)
				prop.SetDateTime(now)
				child.Props.Set(prop)
				modified = true
			}
		}
	}

	if !modified {
		return data, false
	}

	// Re-encode the calendar
	var buf bytes.Buffer
	enc := ical.NewEncoder(&buf)
	if err := enc.Encode(cal); err != nil {
		return data, false
	}

	return buf.Bytes(), true
}

func BuildFreeBusyICS(start, end time.Time, busyIntervals []Interval, prodID string) []byte {
	cal := &ical.Calendar{
		Component: &ical.Component{
			Name:  ical.CompCalendar,
			Props: ical.Props{},
		},
	}

	cal.Props.SetText(ical.PropProductID, prodID)
	cal.Props.SetText(ical.PropVersion, "2.0")

	freeBusy := &ical.Component{
		Name:  ical.CompFreeBusy,
		Props: ical.Props{},
	}

	freeBusy.Props.SetDateTime(ical.PropDateTimeStart, start.UTC())
	freeBusy.Props.SetDateTime(ical.PropDateTimeEnd, end.UTC())

	for _, interval := range busyIntervals {
		prop := ical.NewProp(ical.PropFreeBusy)
		prop.Params.Set("FBTYPE", "BUSY")
		prop.SetText(fmt.Sprintf("%s/%s",
			interval.S.UTC().Format("20060102T150405Z"),
			interval.E.UTC().Format("20060102T150405Z")))
		freeBusy.Props.Add(prop)
	}

	cal.Children = []*ical.Component{freeBusy}

	var buf bytes.Buffer
	enc := ical.NewEncoder(&buf)
	enc.Encode(cal)
	return buf.Bytes()
}
