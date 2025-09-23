package integration

import (
	"encoding/xml"
	"html"
	"regexp"
	"strings"
)

// Minimal Multi-Status parser sufficient for validations (RFC 4918 §13, RFC 6578 adds sync-token)
type multiStatus struct {
	XMLName   xml.Name     `xml:"multistatus"`
	Responses []msResponse `xml:"response"`
	SyncToken string       `xml:"sync-token"`
}
type msResponse struct {
	Href     string     `xml:"href"`
	PropStat []propStat `xml:"propstat"`
	Status   string     `xml:"status"`
}
type propStat struct {
	Status  string `xml:"status"`
	PropRaw anyXML `xml:"prop"`
	// For simplicity, keep raw inner XML for flexible checks
	PropXML string `xml:"-"`
}
type anyXML struct {
	Inner string `xml:",innerxml"`
}

func parseMultiStatus(b []byte) (*multiStatus, error) {
	var ms multiStatus
	if err := xml.Unmarshal(b, &ms); err != nil {
		return nil, err
	}
	// capture inner prop xml for each propstat
	for i := range ms.Responses {
		for j := range ms.Responses[i].PropStat {
			ms.Responses[i].PropStat[j].PropXML = ms.Responses[i].PropStat[j].PropRaw.Inner
		}
	}
	return &ms, nil
}

func statusOK(s string) bool {
	// Typical format: "HTTP/1.1 200 OK"
	return strings.Contains(s, " 200 ")
}

// Light-weight ICS structure checks (RFC 5545)
type icsInfo struct {
	Valid bool
	lines []string
}

func parseICS(s string) icsInfo {
	// Normalize CRLF and unfold folded lines
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	var unfolded []string
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		for i+1 < len(lines) && (strings.HasPrefix(lines[i+1], " ") || strings.HasPrefix(lines[i+1], "\t")) {
			l += strings.TrimLeft(lines[i+1], " \t")
			i++
		}
		unfolded = append(unfolded, strings.TrimRight(l, "\r"))
	}
	info := icsInfo{Valid: false, lines: unfolded}
	if !hasLine(unfolded, "BEGIN:VCALENDAR") || !hasLine(unfolded, "END:VCALENDAR") {
		return info
	}
	info.Valid = true
	return info
}

func (i icsInfo) Has(comp string) bool {
	return hasLine(i.lines, "BEGIN:"+comp) && hasLine(i.lines, "END:"+comp)
}

func (i icsInfo) HasProp(comp string, prop string, contains string) bool {
	inComp := false
	for _, l := range i.lines {
		if l == "BEGIN:"+comp {
			inComp = true
			continue
		}
		if l == "END:"+comp {
			inComp = false
			continue
		}
		if inComp {
			// property may have params: PROP;PARAM=..:value
			if strings.HasPrefix(strings.ToUpper(l), strings.ToUpper(prop)+":") ||
				strings.HasPrefix(strings.ToUpper(l), strings.ToUpper(prop)+";") {
				if contains == "" || strings.Contains(l, contains) {
					return true
				}
			}
		}
	}
	return false
}

func hasLine(lines []string, exact string) bool {
	for _, l := range lines {
		if l == exact {
			return true
		}
	}
	return false
}

var etagRe = regexp.MustCompile(`^(W/)?"[^"]+"$`)

func validETag(s string) bool {
	s = strings.TrimSpace(s)
	return etagRe.MatchString(s)
}

// Extract inner text of <tag>..</tag> (single, naive)
func innerText(xmlStr string, local string) string {
	open := "<" + local
	i := strings.Index(xmlStr, open)
	if i == -1 {
		return ""
	}
	// move to '>' of open tag
	j := strings.Index(xmlStr[i:], ">")
	if j == -1 {
		return ""
	}
	start := i + j + 1
	closeTag := "</" + local + ">"
	k := strings.Index(xmlStr[start:], closeTag)
	if k == -1 {
		return ""
	}
	return xmlStr[start : start+k]
}

func xmlEscape(s string) string {
	repl := strings.NewReplacer(
		`&`, "&amp;",
		`<`, "&lt;",
		`>`, "&gt;",
		`"`, "&quot;",
		`'`, "&apos;",
	)
	return repl.Replace(s)
}
