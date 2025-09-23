package common

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
)

func ServeXML(w http.ResponseWriter) *xml.Encoder {
	w.Header().Set("Content-Type", "application/xml; charset=\"utf-8\"")
	_, _ = w.Write([]byte(xml.Header))
	return xml.NewEncoder(w)
}

func ServeMultiStatus(w http.ResponseWriter, ms *MultiStatus) error {
	w.WriteHeader(http.StatusMultiStatus)
	return ServeXML(w).Encode(ms)
}

func WriteMultiStatus(w http.ResponseWriter, ms MultiStatus) {
	if err := ServeMultiStatus(w, &ms); err != nil {
		http.Error(w, fmt.Sprintf("xml encode error: %v", err), http.StatusInternalServerError)
	}
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

type RawXMLValue struct {
	tok      xml.Token
	children []RawXMLValue
	out      interface{}
}

func NewRawXMLElement(name xml.Name, attr []xml.Attr, children []RawXMLValue) *RawXMLValue {
	return &RawXMLValue{tok: xml.StartElement{Name: name, Attr: attr}, children: children}
}

func EncodeRawXMLElement(v interface{}) (*RawXMLValue, error) {
	return &RawXMLValue{out: v}, nil
}

func (val *RawXMLValue) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	val.tok = start
	val.children = nil
	val.out = nil

	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			child := RawXMLValue{}
			if err := child.UnmarshalXML(d, tok); err != nil {
				return err
			}
			val.children = append(val.children, child)
		case xml.EndElement:
			return nil
		default:
			val.children = append(val.children, RawXMLValue{tok: xml.CopyToken(tok)})
		}
	}
}

func (val *RawXMLValue) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	if val.out != nil {
		return e.Encode(val.out)
	}

	switch tok := val.tok.(type) {
	case xml.StartElement:
		if err := e.EncodeToken(tok); err != nil {
			return err
		}
		for _, child := range val.children {
			if err := child.MarshalXML(e, xml.StartElement{}); err != nil {
				return err
			}
		}
		return e.EncodeToken(tok.End())
	case xml.EndElement:
		panic("unexpected end element")
	default:
		return e.EncodeToken(tok)
	}
}

func (val *RawXMLValue) XMLName() (name xml.Name, ok bool) {
	if start, ok := val.tok.(xml.StartElement); ok {
		return start.Name, true
	}
	return xml.Name{}, false
}

func (val *RawXMLValue) Decode(v interface{}) error {
	if val.out != nil {
		return fmt.Errorf("cannot decode marshal-only XML value")
	}
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	if err := val.MarshalXML(enc, xml.StartElement{}); err != nil {
		return err
	}
	_ = enc.Flush()
	return xml.Unmarshal(buf.Bytes(), v)
}
