package common

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
)

const (
	NSDAV    = "DAV:"
	NSCalDAV = "urn:ietf:params:xml:ns:caldav"
)

func WriteMultiStatus(w http.ResponseWriter, ms MultiStatus) {
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(ms); err != nil {
		http.Error(w, fmt.Sprintf("xml encode error: %v", err), http.StatusInternalServerError)
		return
	}
	_ = enc.Flush()
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(207)
	_, _ = w.Write(buf.Bytes())
}

func Ok() string { return "HTTP/1.1 200 OK" }

func MakeCalendarResourcetype() *ResourceType {
	return &ResourceType{
		Collection: &struct{}{},
		Calendar:   &struct{}{},
	}
}
func MakeCollectionResourcetype() *ResourceType {
	return &ResourceType{
		Collection: &struct{}{},
	}
}
func MakePrincipalResourcetype() *ResourceType {
	return &ResourceType{
		Principal:  &struct{}{},
		Collection: nil,
	}
}

func CalContentType() *string {
	ct := "text/calendar; charset=utf-8"
	return &ct
}

