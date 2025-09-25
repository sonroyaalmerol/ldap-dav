package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCardDAVIntegration(t *testing.T) {
	t.Parallel()

	// Env-driven config
	httpAddr := os.Getenv("HTTP_ADDR")
	if httpAddr == "" {
		httpAddr = ":8081"
	}
	hostPort := "127.0.0.1" + httpAddr
	baseURL := "http://" + hostPort
	basePath := os.Getenv("HTTP_BASE_PATH")
	if basePath == "" {
		basePath = "/dav"
	}

	// Start server subprocess
	cmd := exec.Command("/usr/local/bin/ldap-dav")
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	time.Sleep(200 * time.Millisecond)
	waitPort(t, hostPort, 10*time.Second)

	client := &http.Client{Timeout: 10 * time.Second}
	authz := basicAuth("alice", "password")
	_ = context.Background()

	// Run all test sections
	t.Run("WellKnownRedirectCardDAV", func(t *testing.T) {
		testWellKnownRedirectCardDAV(t, baseURL, basePath)
	})

	t.Run("OptionsCardDAV", func(t *testing.T) {
		testOptionsCardDAV(t, client, baseURL, basePath)
	})

	t.Run("PrincipalPropfindCardDAV", func(t *testing.T) {
		testPrincipalPropfindCardDAV(t, client, baseURL, basePath, authz)
	})

	t.Run("AddressbookHomeListing", func(t *testing.T) {
		testAddressbookHomeListing(t, client, baseURL, basePath, authz)
	})

	t.Run("PersonalAddressbookCRUD", func(t *testing.T) {
		testPersonalAddressbookCRUD(t, client, baseURL, basePath, authz)
	})

	t.Run("LDAPAddressbookReadOnly", func(t *testing.T) {
		testLDAPAddressbookReadOnly(t, client, baseURL, basePath, authz)
	})

	t.Run("AddressbookReports", func(t *testing.T) {
		testAddressbookReports(t, client, baseURL, basePath, authz)
	})

	t.Run("AddressbookSync", func(t *testing.T) {
		testAddressbookSync(t, client, baseURL, basePath, authz)
	})

	t.Run("AddressbookCollectionProperties", func(t *testing.T) {
		testAddressbookCollectionProperties(t, client, baseURL, basePath, authz)
	})

	t.Run("CardDALErrorConditions", func(t *testing.T) {
		testCardDALErrorConditions(t, client, baseURL, basePath, authz)
	})

	t.Run("LargeAddressbookHandling", func(t *testing.T) {
		testLargeAddressbookHandling(t, client, baseURL, basePath, authz)
	})
}

// Tests

func testWellKnownRedirectCardDAV(t *testing.T, baseURL, basePath string) {
	redirClient := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, _ := http.NewRequest("GET", baseURL+"/.well-known/carddav", nil)
	resp, err := redirClient.Do(req)
	if err != nil {
		t.Fatalf("well-known carddav: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusPermanentRedirect && resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("well-known carddav status: %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		t.Fatalf("well-known carddav missing Location header")
	}
	if loc != basePath+"/" && loc != "/dav/" {
		t.Logf("well-known Location: %s", loc)
	}
}

func testOptionsCardDAV(t *testing.T, client *http.Client, baseURL, basePath string) {
	url := baseURL + basePath + "/addressbooks/"
	req, _ := http.NewRequest("OPTIONS", url, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("options carddav: %v", err)
	}
	defer resp.Body.Close()
	got := resp.Header.Get("DAV")
	if got == "" || !bytes.Contains([]byte(got), []byte("addressbook-access")) {
		t.Fatalf("DAV header missing addressbook-access at %s: %q", url, got)
	}
	allow := strings.ToUpper(resp.Header.Get("Allow"))
	for _, m := range []string{"PROPFIND", "REPORT", "MKCOL", "OPTIONS"} {
		if !strings.Contains(allow, m) {
			t.Logf("Allow header missing %s (got %q)", m, allow)
		}
	}
}

func testPrincipalPropfindCardDAV(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	url := baseURL + basePath + "/principals/users/alice"
	req, _ := http.NewRequest("PROPFIND", url, nil)
	req.Header.Set("Authorization", authz)
	req.Header.Set("Depth", "0")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("propfind principal carddav: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 207 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("propfind principal carddav status at %s: %d body=%s", url, resp.StatusCode, string(b))
	}
	body, _ := io.ReadAll(resp.Body)
	ms, err := parseMultiStatus(body)
	if err != nil {
		t.Fatalf("parse principal multistatus: %v\n%s", err, string(body))
	}
	if len(ms.Responses) == 0 {
		t.Fatalf("principal multistatus has no responses")
	}
	// Verify CARDDAV:addressbook-home-set
	foundHome := false
	for _, r := range ms.Responses {
		for _, ps := range r.PropStat {
			if statusOK(ps.Status) && strings.Contains(ps.PropXML, "addressbook-home-set") {
				foundHome = true
			}
		}
	}
	if !foundHome {
		t.Log("principal lacks CARDDAV:addressbook-home-set (server may expose elsewhere)")
	}
}

func testAddressbookHomeListing(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	url := baseURL + basePath + "/addressbooks/alice/"
	req, _ := http.NewRequest("PROPFIND", url, nil)
	req.Header.Set("Authorization", authz)
	req.Header.Set("Depth", "1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("propfind addressbook home: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 207 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("propfind addressbook home status at %s: %d body=%s", url, resp.StatusCode, string(b))
	}
	b, _ := io.ReadAll(resp.Body)
	if _, err := parseMultiStatus(b); err != nil {
		t.Fatalf("parse addressbook home multistatus: %v", err)
	}
}

func testPersonalAddressbookCRUD(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	// Ensure personal addressbook exists (MKCOL)
	abName := "personal"
	abURL := baseURL + basePath + "/addressbooks/alice/" + encSeg(abName) + "/"

	// Create collection if missing
	{
		body := `<?xml version="1.0" encoding="utf-8" ?>
<D:mkcol xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
  <D:set>
    <D:prop>
      <D:resourcetype>
        <D:collection/>
        <C:addressbook/>
      </D:resourcetype>
      <D:displayname>Personal Addressbook</D:displayname>
    </D:prop>
  </D:set>
</D:mkcol>`
		req, _ := http.NewRequest("MKCOL", abURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusConflict {
				t.Fatalf("mkcol addressbook status: %d", resp.StatusCode)
			}
		}
	}

	// PUT a vCard
	card := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Alice Example\r\nN:Example;Alice;;;\r\nUID:contact1\r\nEMAIL;TYPE=INTERNET:alice@example.com\r\nEND:VCARD\r\n"
	contactURL := abURL + "contact1.vcf"
	var etag string
	{
		req, _ := http.NewRequest("PUT", contactURL, bytes.NewBufferString(card))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/vcard; charset=utf-8")
		req.Header.Set("If-None-Match", "*")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("put vcard: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("put vcard status: %d body=%s", resp.StatusCode, string(b))
		}
		etag = resp.Header.Get("ETag")
		if etag == "" || !validETag(etag) {
			t.Fatalf("missing/invalid ETag on PUT: %q", etag)
		}
	}

	// GET the vCard
	{
		req, _ := http.NewRequest("GET", contactURL, nil)
		req.Header.Set("Authorization", authz)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("get vcard: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("get vcard status: %d", resp.StatusCode)
		}
		ct := strings.ToLower(resp.Header.Get("Content-Type"))
		if !strings.HasPrefix(ct, "text/vcard") {
			t.Errorf("GET text/vcard content-type: %q", ct)
		}
		gotETag := resp.Header.Get("ETag")
		if gotETag == "" || !validETag(gotETag) {
			t.Errorf("GET missing/invalid ETag: %q", gotETag)
		}
	}

	// REPORT addressbook-query
	{
		body := `<?xml version="1.0" encoding="utf-8" ?>
<C:addressbook-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
 <D:prop>
  <D:getetag/>
  <C:address-data/>
 </D:prop>
 <C:filter>
  <C:prop-filter name="FN">
    <C:text-match>alice</C:text-match>
  </C:prop-filter>
 </C:filter>
</C:addressbook-query>`
		req, _ := http.NewRequest("REPORT", abURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("addressbook-query: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("addressbook-query status: %d", resp.StatusCode)
		}
		rb, _ := io.ReadAll(resp.Body)
		ms, err := parseMultiStatus(rb)
		if err != nil {
			t.Fatalf("parse addressbook-query multistatus: %v\n%s", err, string(rb))
		}
		if len(ms.Responses) == 0 {
			t.Fatalf("addressbook-query returned no responses")
		}
	}

	// DELETE the vCard
	{
		req, _ := http.NewRequest("DELETE", contactURL, nil)
		req.Header.Set("Authorization", authz)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("delete vcard: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
			t.Fatalf("delete vcard status: %d", resp.StatusCode)
		}
	}
}

func testLDAPAddressbookReadOnly(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	// Assumes LDAP addressbook is exposed as ldap_0
	ldapAB := "ldap_0"
	abURL := baseURL + basePath + "/addressbooks/alice/" + ldapAB + "/"

	// PROPFIND collection depth 0
	{
		body := `<?xml version="1.0" encoding="utf-8" ?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
 <D:prop>
  <D:resourcetype/>
  <D:displayname/>
  <D:supported-report-set/>
 </D:prop>
</D:propfind>`
		req, _ := http.NewRequest("PROPFIND", abURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		req.Header.Set("Depth", "0")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("propfind ldap ab: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("propfind ldap ab status: %d", resp.StatusCode)
		}
	}

	// REPORT addressbook-query to list contacts
	{
		body := `<?xml version="1.0" encoding="utf-8" ?>
<C:addressbook-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
 <D:prop>
  <C:address-data/>
 </D:prop>
</C:addressbook-query>`
		req, _ := http.NewRequest("REPORT", abURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("ldap addressbook-query: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("ldap addressbook-query status: %d", resp.StatusCode)
		}
	}

	// PUT should be rejected (read-only)
	{
		card := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Should Fail\r\nN:Fail;Should;;;\r\nUID:should-fail\r\nEMAIL:fail@example.com\r\nEND:VCARD\r\n"
		req, _ := http.NewRequest("PUT", abURL+"should-fail.vcf", bytes.NewBufferString(card))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/vcard; charset=utf-8")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("ldap put: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected read-only rejection, got: %d", resp.StatusCode)
		}
	}
}

func testAddressbookReports(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	abURL := baseURL + basePath + "/addressbooks/alice/personal/"

	// Ensure a couple of contacts
	for i := 1; i <= 2; i++ {
		card := fmt.Sprintf("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Person %d\r\nN:Person;%d;;;\r\nUID:person-%d\r\nEMAIL:p%d@example.com\r\nEND:VCARD\r\n", i, i, i, i)
		req, _ := http.NewRequest("PUT", abURL+fmt.Sprintf("person-%d.vcf", i), bytes.NewBufferString(card))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/vcard; charset=utf-8")
		_, _ = client.Do(req)
	}

	// addressbook-query filtering by FN
	{
		body := `<?xml version="1.0" encoding="utf-8" ?>
<C:addressbook-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
 <D:prop>
  <D:getetag/>
  <C:address-data/>
 </D:prop>
 <C:filter>
  <C:prop-filter name="FN">
    <C:text-match>Person</C:text-match>
  </C:prop-filter>
 </C:filter>
</C:addressbook-query>`
		req, _ := http.NewRequest("REPORT", abURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("addressbook-query fn: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("addressbook-query fn status: %d", resp.StatusCode)
		}
	}

	// addressbook-multiget
	{
		body := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8" ?>
<C:addressbook-multiget xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
 <D:prop>
  <D:getetag/>
  <C:address-data/>
 </D:prop>
 <D:href>%s/addressbooks/alice/personal/person-1.vcf</D:href>
 <D:href>%s/addressbooks/alice/personal/person-2.vcf</D:href>
</C:addressbook-multiget>`, basePath, basePath)
		req, _ := http.NewRequest("REPORT", abURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("addressbook-multiget: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("addressbook-multiget status: %d", resp.StatusCode)
		}
	}
}

func testAddressbookSync(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	abURL := baseURL + basePath + "/addressbooks/alice/personal/"

	// Initial sync
	{
		body := `<?xml version="1.0" encoding="utf-8" ?>
<D:sync-collection xmlns:D="DAV:"><D:sync-token/></D:sync-collection>`
		req, _ := http.NewRequest("REPORT", abURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("initial sync: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("initial sync status: %d", resp.StatusCode)
		}
		rb, _ := io.ReadAll(resp.Body)
		ms, err := parseMultiStatus(rb)
		if err != nil {
			t.Fatalf("parse sync initial: %v", err)
		}
		if ms.SyncToken == "" {
			t.Fatalf("missing sync-token")
		}
		// Add a contact then re-sync
		card := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Sync Person\r\nN:Person;Sync;;;\r\nUID:sync-person\r\nEMAIL:sync@example.com\r\nEND:VCARD\r\n"
		put, _ := http.NewRequest("PUT", abURL+"sync-person.vcf", bytes.NewBufferString(card))
		put.Header.Set("Authorization", authz)
		put.Header.Set("Content-Type", "text/vcard; charset=utf-8")
		_, _ = client.Do(put)

		req2, _ := http.NewRequest("REPORT", abURL, bytes.NewBufferString(body))
		req2.Header.Set("Authorization", authz)
		req2.Header.Set("Content-Type", "application/xml")
		resp2, err := client.Do(req2)
		if err != nil {
			t.Fatalf("second sync: %v", err)
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != 207 {
			t.Fatalf("second sync status: %d", resp2.StatusCode)
		}
	}
}

func testAddressbookCollectionProperties(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	url := baseURL + basePath + "/addressbooks/alice/personal/"
	body := `<?xml version="1.0" encoding="utf-8" ?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
 <D:prop>
  <D:resourcetype/>
  <D:displayname/>
  <C:supported-address-data/>
  <C:max-resource-size/>
  <D:supported-report-set/>
 </D:prop>
</D:propfind>`
	req, _ := http.NewRequest("PROPFIND", url, bytes.NewBufferString(body))
	req.Header.Set("Authorization", authz)
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Depth", "0")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("propfind collection properties: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 207 {
		t.Fatalf("collection properties status: %d", resp.StatusCode)
	}
	respBody, _ := io.ReadAll(resp.Body)
	ms, err := parseMultiStatus(respBody)
	if err != nil {
		t.Fatalf("parse collection properties: %v", err)
	}
	if len(ms.Responses) == 0 {
		t.Fatalf("no response in multistatus")
	}
	// Ensure supported-report-set includes addressbook-query, addressbook-multiget, sync-collection
	flat := ""
	for _, r := range ms.Responses {
		for _, ps := range r.PropStat {
			flat += ps.PropXML
		}
	}
	for _, rep := range []string{"addressbook-query", "addressbook-multiget", "sync-collection"} {
		if !strings.Contains(flat, rep) {
			t.Logf("supported-report-set missing %s", rep)
		}
	}
}

func testCardDALErrorConditions(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	abURL := baseURL + basePath + "/addressbooks/alice/personal/"

	// Invalid vCard data
	t.Run("InvalidVCardData", func(t *testing.T) {
		invalid := "NOT A VCARD"
		url := abURL + "invalid.vcf"
		req, _ := http.NewRequest("PUT", url, bytes.NewBufferString(invalid))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/vcard; charset=utf-8")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("put invalid vcard: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode < 400 || resp.StatusCode >= 500 {
			t.Fatalf("expected 4xx for invalid vcard, got: %d", resp.StatusCode)
		}
	})

	// Unauthorized access
	t.Run("UnauthorizedHome", func(t *testing.T) {
		url := baseURL + basePath + "/addressbooks/alice/"
		req, _ := http.NewRequest("PROPFIND", url, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("unauthorized propfind: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401 unauthorized, got: %d", resp.StatusCode)
		}
	})

	// Non-existent resources
	t.Run("NonExistentContact", func(t *testing.T) {
		url := abURL + "does-not-exist.vcf"
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", authz)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("get nonexistent: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404 for nonexistent contact, got: %d", resp.StatusCode)
		}
	})

	// Malformed XML in REPORT
	t.Run("MalformedXMLReport", func(t *testing.T) {
		body := `<invalid-xml>`
		req, _ := http.NewRequest("REPORT", abURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("malformed xml report: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode < 400 || resp.StatusCode >= 500 {
			t.Fatalf("expected 4xx for malformed XML, got: %d", resp.StatusCode)
		}
	})

	// Unsupported methods
	t.Run("UnsupportedMethods", func(t *testing.T) {
		unsupported := []string{"PATCH", "TRACE"}
		for _, m := range unsupported {
			req, _ := http.NewRequest(m, abURL, nil)
			req.Header.Set("Authorization", authz)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("unsupported method %s: %v", m, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusNotImplemented {
				t.Logf("method %s returned %d (expected 405 or 501)", m, resp.StatusCode)
			}
		}
	})
}

func testLargeAddressbookHandling(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	abURL := baseURL + basePath + "/addressbooks/alice/personal/"

	// Create many contacts (reduced count for runtime; adjust as needed)
	t.Run("CreateManyContacts", func(t *testing.T) {
		num := 2000
		for i := 0; i < num; i++ {
			card := fmt.Sprintf("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Person %d\r\nN:Person;%d;;;\r\nUID:bulk-%d\r\nEMAIL:b%d@example.com\r\nEND:VCARD\r\n", i, i, i, i)
			req, _ := http.NewRequest("PUT", abURL+fmt.Sprintf("bulk-%d.vcf", i), bytes.NewBufferString(card))
			req.Header.Set("Authorization", authz)
			req.Header.Set("Content-Type", "text/vcard; charset=utf-8")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("create contact %d: %v", i, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
				t.Fatalf("create contact %d status: %d", i, resp.StatusCode)
			}
		}
	})

	// PROPFIND depth 1 performance
	t.Run("PropfindLargeCollection", func(t *testing.T) {
		req, _ := http.NewRequest("PROPFIND", abURL, nil)
		req.Header.Set("Authorization", authz)
		req.Header.Set("Depth", "1")
		req.Header.Set("Content-Type", "application/xml")
		start := time.Now()
		resp, err := client.Do(req)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("propfind large addressbook: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("propfind large addressbook status: %d", resp.StatusCode)
		}
		if elapsed > 30*time.Second {
			t.Logf("PROPFIND on large addressbook took %v", elapsed)
		}
	})

	// addressbook-query without filters
	t.Run("AddressbookQueryAll", func(t *testing.T) {
		body := `<?xml version="1.0" encoding="utf-8" ?>
<C:addressbook-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
 <D:prop>
  <D:getetag/>
 </D:prop>
 <C:filter/>
</C:addressbook-query>`
		req, _ := http.NewRequest("REPORT", abURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		start := time.Now()
		resp, err := client.Do(req)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("addressbook-query all: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("addressbook-query all status: %d body=%s", resp.StatusCode, string(b))
		}
		if elapsed > 15*time.Second {
			t.Logf("addressbook-query all took %v", elapsed)
		}
	})
}

// Helpers

func encSeg(seg string) string {
	return strings.ReplaceAll(url.PathEscape(seg), "+", "%20")
}
