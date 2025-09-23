package integration

import (
	"bytes"
	"encoding/xml"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
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

func getETag(t *testing.T, client *http.Client, resourceURL, authz string) string {
	t.Helper()
	req, _ := http.NewRequest("HEAD", resourceURL, nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("HEAD for ETag %s: %v", resourceURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD for ETag status at %s: %d", resourceURL, resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatalf("missing ETag on HEAD for %s", resourceURL)
	}
	return etag
}

func currentSyncToken(t *testing.T, client *http.Client, collectionURL, authz string) string {
	t.Helper()
	body := `<?xml version="1.0" encoding="utf-8" ?>
<D:sync-collection xmlns:D="DAV:"><D:sync-token/></D:sync-collection>`
	req, _ := http.NewRequest("REPORT", collectionURL, bytes.NewBufferString(body))
	req.Header.Set("Authorization", authz)
	req.Header.Set("Content-Type", "application/xml")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("sync-collection (get token) %s: %v", collectionURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 207 {
		t.Fatalf("sync-collection status at %s: %d", collectionURL, resp.StatusCode)
	}
	rb, _ := io.ReadAll(resp.Body)
	ms, err := parseMultiStatus(rb)
	if err != nil {
		t.Fatalf("parse sync token multistatus: %v", err)
	}
	if ms.SyncToken == "" {
		t.Fatalf("missing DAV:sync-token for %s", collectionURL)
	}
	return ms.SyncToken
}

func verifyDeletionReflectedInSync(t *testing.T, client *http.Client, collectionURL, authz, prevToken, deletedHref string) {
	t.Helper()
	body := `<?xml version="1.0" encoding="utf-8" ?>
<D:sync-collection xmlns:D="DAV:">
  <D:sync-token>` + xmlEscape(prevToken) + `</D:sync-token>
  <D:prop><D:getetag/></D:prop>
</D:sync-collection>`
	req, _ := http.NewRequest("REPORT", collectionURL, bytes.NewBufferString(body))
	req.Header.Set("Authorization", authz)
	req.Header.Set("Content-Type", "application/xml")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("sync-collection after deletion: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 207 {
		t.Fatalf("sync-collection after deletion status: %d", resp.StatusCode)
	}
	rb, _ := io.ReadAll(resp.Body)
	ms, err := parseMultiStatus(rb)
	if err != nil {
		t.Fatalf("parse multistatus after deletion: %v\n%s", err, string(rb))
	}
	found := false
	for _, r := range ms.Responses {
		if strings.Contains(r.Href, deletedHref) {
			// deletion is represented either as 404 status on the response or a propstat 404
			if strings.Contains(strings.ToLower(r.Status), "404") {
				found = true
				break
			}
			for _, ps := range r.PropStat {
				if strings.Contains(strings.ToLower(ps.Status), "404") {
					found = true
					break
				}
			}
		}
	}
	if !found {
		// fallback: raw body contains href and 404
		if !(strings.Contains(string(rb), deletedHref) && strings.Contains(string(rb), "404")) {
			t.Fatalf("deleted resource not reflected in sync-collection changes for %s\n%s", deletedHref, string(rb))
		}
	}
}

func parentCollectionURL(resourceURL string) (string, string) {
	// returns collectionURL (ending in "/") and href path component for the resource
	u, err := url.Parse(resourceURL)
	if err != nil {
		return "", ""
	}
	path := u.Path
	if strings.HasSuffix(path, "/") {
		path = strings.TrimSuffix(path, "/")
	}
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return "", ""
	}
	collPath := path[:i+1]
	href := path
	u.Path = collPath
	return u.String(), href
}

func deleteAndValidate(t *testing.T, client *http.Client, resourceURL, authz string) {
	t.Helper()
	collURL, href := parentCollectionURL(resourceURL)
	if collURL == "" || href == "" {
		t.Fatalf("cannot derive collection from %s", resourceURL)
	}
	prevToken := currentSyncToken(t, client, collURL, authz)
	etag := getETag(t, client, resourceURL, authz)
	req, _ := http.NewRequest("DELETE", resourceURL, nil)
	req.Header.Set("Authorization", authz)
	req.Header.Set("If-Match", etag)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("delete %s: %v", resourceURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete status at %s: %d body=%s", resourceURL, resp.StatusCode, string(b))
	}
	getReq, _ := http.NewRequest("GET", resourceURL, nil)
	getReq.Header.Set("Authorization", authz)
	getResp, err := client.Do(getReq)
	if err != nil {
		t.Fatalf("get after delete %s: %v", resourceURL, err)
	}
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", getResp.StatusCode)
	}
	verifyDeletionReflectedInSync(t, client, collURL, authz, prevToken, href)
}
