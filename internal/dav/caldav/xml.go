package caldav

import (
	"encoding/xml"
	"net/http"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
)

func (h *Handlers) parseMkcolRequest(body []byte) (displayName, description, color string) {
	type mkcolProp struct {
		XMLName      xml.Name `xml:"DAV: prop"`
		DisplayName  *string  `xml:"DAV: displayname"`
		Description  *string  `xml:"urn:ietf:params:xml:ns:caldav calendar-description"`
		ResourceType struct {
			Calendar *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar"`
		} `xml:"DAV: resourcetype"`
		Raw []common.RawXMLValue `xml:",any"`
	}
	var mkcolReq struct {
		XMLName xml.Name `xml:"DAV: mkcol"`
		Set     *struct {
			XMLName xml.Name  `xml:"DAV: set"`
			Prop    mkcolProp `xml:"DAV: prop"`
		} `xml:"DAV: set"`
	}

	if len(body) > 0 {
		if err := xml.Unmarshal(body, &mkcolReq); err == nil && mkcolReq.Set != nil {
			if mkcolReq.Set.Prop.DisplayName != nil {
				displayName = *mkcolReq.Set.Prop.DisplayName
			}
			if mkcolReq.Set.Prop.Description != nil {
				description = *mkcolReq.Set.Prop.Description
			}
			color = h.extractColorFromRaw(mkcolReq.Set.Prop.Raw)
		}
	}

	if color == "" || !common.IsValidHexColor(color) {
		color = "#3174ad"
	}

	return
}

func (h *Handlers) parseMkcalendarRequest(body []byte) (displayName, description, color string) {
	type mkcalProp struct {
		XMLName             xml.Name             `xml:"DAV: prop"`
		DisplayName         *string              `xml:"DAV: displayname"`
		CalendarDescription *string              `xml:"urn:ietf:params:xml:ns:caldav calendar-description"`
		Raw                 []common.RawXMLValue `xml:",any"`
	}
	var mkcalReq struct {
		XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav mkcalendar"`
		Set     *struct {
			XMLName xml.Name  `xml:"DAV: set"`
			Prop    mkcalProp `xml:"DAV: prop"`
		} `xml:"DAV: set"`
	}

	if len(body) > 0 {
		if err := xml.Unmarshal(body, &mkcalReq); err == nil && mkcalReq.Set != nil {
			if mkcalReq.Set.Prop.DisplayName != nil {
				displayName = *mkcalReq.Set.Prop.DisplayName
			}
			if mkcalReq.Set.Prop.CalendarDescription != nil {
				description = *mkcalReq.Set.Prop.CalendarDescription
			}
			color = h.extractColorFromRaw(mkcalReq.Set.Prop.Raw)
		}
	}

	if color == "" || !common.IsValidHexColor(color) {
		color = "#3174ad"
	}

	return
}

func (h *Handlers) parseProppatchRequest(body []byte) (newName *string, newColor string, hasColorUpdate bool) {
	type setRemoveProp struct {
		DisplayName *string              `xml:"DAV: displayname"`
		Raw         []common.RawXMLValue `xml:",any"`
	}
	type setRemove struct {
		XMLName xml.Name
		Prop    setRemoveProp `xml:"DAV: prop"`
	}
	var req struct {
		XMLName xml.Name   `xml:"DAV: propertyupdate"`
		Set     *setRemove `xml:"DAV: set"`
		Remove  *setRemove `xml:"DAV: remove"`
	}

	if len(body) == 0 || xml.Unmarshal(body, &req) != nil {
		return
	}

	if req.Set != nil {
		newName = req.Set.Prop.DisplayName
		if color := h.extractColorFromRaw(req.Set.Prop.Raw); color != "" {
			newColor = color
			hasColorUpdate = true
		}
	}

	if req.Remove != nil {
		if req.Remove.Prop.DisplayName != nil {
			empty := ""
			newName = &empty
		}
		if h.hasColorInRaw(req.Remove.Prop.Raw) {
			newColor = "#3174ad"
			hasColorUpdate = true
		}
	}

	return
}

func (h *Handlers) extractColorFromRaw(raw []common.RawXMLValue) string {
	for _, rawProp := range raw {
		var colorProp struct {
			XMLName xml.Name `xml:"http://apple.com/ns/ical/ calendar-color"`
			Text    string   `xml:",chardata"`
		}

		xmlBytes, err := xml.Marshal(&rawProp)
		if err != nil {
			continue
		}

		if err := xml.Unmarshal(xmlBytes, &colorProp); err == nil {
			if colorProp.XMLName.Space == "http://apple.com/ns/ical/" && colorProp.XMLName.Local == "calendar-color" {
				color := colorProp.Text
				if len(color) == 9 && color[0] == '#' {
					color = color[:7]
				}
				return color
			}
		}
	}
	return ""
}

func (h *Handlers) hasColorInRaw(raw []common.RawXMLValue) bool {
	for _, rawProp := range raw {
		xmlBytes, err := xml.Marshal(&rawProp)
		if err != nil {
			continue
		}

		var colorProp struct {
			XMLName xml.Name `xml:"http://apple.com/ns/ical/ calendar-color"`
		}

		if err := xml.Unmarshal(xmlBytes, &colorProp); err == nil {
			if colorProp.XMLName.Space == "http://apple.com/ns/ical/" && colorProp.XMLName.Local == "calendar-color" {
				return true
			}
		}
	}
	return false
}

func (h *Handlers) writeProppatchResponse(w http.ResponseWriter, path string, newName *string, newColor string, hasColorUpdate bool, displayNameStatus, colorStatus int) {
	resp := common.Response{
		Hrefs: []common.Href{{Value: path}},
	}

	if newName != nil {
		propValue := *newName
		_ = resp.EncodeProp(displayNameStatus, common.DisplayName{Name: propValue})
	}

	if hasColorUpdate {
		if colorStatus == http.StatusOK {
			_ = resp.EncodeProp(colorStatus, struct {
				XMLName xml.Name `xml:"http://apple.com/ns/ical/ calendar-color"`
				Text    string   `xml:",chardata"`
			}{Text: newColor})
		} else {
			_ = resp.EncodeProp(colorStatus, struct {
				XMLName xml.Name `xml:"http://apple.com/ns/ical/ calendar-color"`
			}{})
		}
	}

	ms := common.MultiStatus{Responses: []common.Response{resp}}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		h.logger.Error().Err(err).Msg("failed to serve MultiStatus for PROPPATCH")
	}
}
