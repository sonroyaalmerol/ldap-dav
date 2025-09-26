package carddav

import (
	"encoding/xml"
	"fmt"
	"hash/fnv"
	"net/http"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
)

func buildReportResponseLDAP(hrefStr string, props common.PropRequest, contact *directory.Contact) common.Response {
	vcardStr := contact.VCardData
	etag := computeStableETag(contact)

	resp := common.Response{Hrefs: []common.Href{{Value: hrefStr}}}
	_ = resp.EncodeProp(http.StatusOK, common.GetContentType{Type: "text/vcard; charset=utf-8"})
	if props.AddressData {
		type AddressData struct {
			XMLName xml.Name `xml:"urn:ietf:params:xml:ns:carddav address-data"`
			Text    string   `xml:",chardata"`
		}
		_ = resp.EncodeProp(http.StatusOK, AddressData{Text: vcardStr})
	}
	if props.GetETag && etag != "" {
		_ = resp.EncodeProp(http.StatusOK, common.GetETag{ETag: common.ETag(etag)})
	}
	return resp
}

func computeStableETag(ct *directory.Contact) string {
	h := fnv.New64a()
	write := func(s string) { _, _ = h.Write([]byte(s)); _, _ = h.Write([]byte{0}) }
	write(ct.DN)
	write(ct.DisplayName)
	write(ct.FirstName)
	write(ct.LastName)
	for _, e := range ct.Email {
		write(e)
	}
	for _, p := range ct.Phone {
		write(p)
	}
	write(ct.Organization)
	write(ct.Title)
	return fmt.Sprintf("%x", h.Sum(nil))
}
