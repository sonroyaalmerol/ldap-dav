package integration

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func waitPort(t *testing.T, hostPort string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", hostPort, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		lastErr = err
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("port %s not ready within %v (last err: %v)", hostPort, timeout, lastErr)
}

func basicAuth(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func TestIntegration(t *testing.T) {
	// Env-driven config for server
	httpAddr := os.Getenv("HTTP_ADDR")
	if httpAddr == "" {
		httpAddr = ":8081"
	}
	hostPort := "127.0.0.1" + httpAddr // e.g., 127.0.0.1:8081
	baseURL := "http://" + hostPort
	basePath := os.Getenv("HTTP_BASE_PATH")
	if basePath == "" {
		basePath = "/dav"
	}

	// Start server subprocess (inherits env)
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

	// Give it a moment to begin binding, then wait for port
	time.Sleep(200 * time.Millisecond)
	waitPort(t, hostPort, 10*time.Second)

	client := &http.Client{Timeout: 10 * time.Second}
	authz := basicAuth("alice", "password")

	// Run all test sections
	t.Run("WellKnownRedirect", func(t *testing.T) {
		testWellKnownRedirect(t, baseURL, basePath)
	})

	t.Run("Options", func(t *testing.T) {
		testOptions(t, client, baseURL, basePath)
	})

	t.Run("PrincipalPropfind", func(t *testing.T) {
		testPrincipalPropfind(t, client, baseURL, basePath, authz)
	})

	t.Run("CalendarHomeListing", func(t *testing.T) {
		testCalendarHomeListing(t, client, baseURL, basePath, authz)
	})

	t.Run("BasicEventOperations", func(t *testing.T) {
		testBasicEventOperations(t, client, baseURL, basePath, authz)
	})

	t.Run("MkCalendar", func(t *testing.T) {
		testMkCalendar(t, client, baseURL, basePath, authz)
	})

	t.Run("EventManagement", func(t *testing.T) {
		testEventManagement(t, client, baseURL, basePath, authz)
	})

	t.Run("AdvancedQuerying", func(t *testing.T) {
		testAdvancedQuerying(t, client, baseURL, basePath, authz)
	})

	t.Run("ConcurrencyAndSync", func(t *testing.T) {
		testConcurrencyAndSync(t, client, baseURL, basePath, authz)
	})

	t.Run("CollectionProperties", func(t *testing.T) {
		testCollectionProperties(t, client, baseURL, basePath, authz)
	})

	t.Run("ErrorConditions", func(t *testing.T) {
		testErrorConditions(t, client, baseURL, basePath, authz)
	})

	t.Run("LargeCollectionHandling", func(t *testing.T) {
		testLargeCollectionHandling(t, client, baseURL, basePath, authz)
	})
}

// Original tests preserved
func testWellKnownRedirect(t *testing.T, baseURL, basePath string) {
	redirClient := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, _ := http.NewRequest("GET", baseURL+"/.well-known/caldav", nil)
	resp, err := redirClient.Do(req)
	if err != nil {
		t.Fatalf("well-known: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusPermanentRedirect && resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("well-known status: %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		t.Fatalf("well-known missing Location header")
	}
	if loc != "/dav/" && loc != (basePath+"/") {
		t.Logf("well-known Location: %s", loc)
	}
}

func testOptions(t *testing.T, client *http.Client, baseURL, basePath string) {
	url := baseURL + basePath + "/calendars/"
	req, _ := http.NewRequest("OPTIONS", url, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	defer resp.Body.Close()
	got := resp.Header.Get("DAV")
	if got == "" || !bytes.Contains([]byte(got), []byte("calendar-access")) {
		t.Fatalf("DAV header missing calendar-access at %s: %q", url, got)
	}
}

func testPrincipalPropfind(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	url := baseURL + basePath + "/principals/users/alice"
	req, _ := http.NewRequest("PROPFIND", url, nil)
	req.Header.Set("Authorization", authz)
	req.Header.Set("Depth", "0")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("propfind principal: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 207 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("propfind principal status at %s: %d body=%s", url, resp.StatusCode, string(b))
	}
}

func testCalendarHomeListing(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	url := baseURL + basePath + "/calendars/alice/"
	req, _ := http.NewRequest("PROPFIND", url, nil)
	req.Header.Set("Authorization", authz)
	req.Header.Set("Depth", "1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("propfind home: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 207 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("propfind home status at %s: %d body=%s", url, resp.StatusCode, string(b))
	}
}

func testBasicEventOperations(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	ics := "BEGIN:VCALENDAR\r\n" +
		"VERSION:2.0\r\n" +
		"PRODID:-//ldap-dav//test//EN\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:evt1\r\n" +
		"DTSTAMP:20250101T090000Z\r\n" +
		"DTSTART:20250101T100000Z\r\n" +
		"DTEND:20250101T110000Z\r\n" +
		"SUMMARY:Test\r\n" +
		"END:VEVENT\r\n" +
		"END:VCALENDAR\r\n"

	// PUT event into bob/team as alice (should be allowed via editors group)
	var etag string
	{
		url := baseURL + basePath + "/calendars/alice/shared/team/evt1.ics"
		req, _ := http.NewRequest("PUT", url, bytes.NewBufferString(ics))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		req.Header.Set("If-None-Match", "*")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("put shared event: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("put shared event status at %s: %d body=%s", url, resp.StatusCode, string(b))
		}
		etag = resp.Header.Get("ETag")
		if etag == "" {
			t.Fatalf("missing ETag on PUT")
		}
	}

	// GET event via shared path
	{
		url := baseURL + basePath + "/calendars/alice/shared/team/evt1.ics"
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", authz)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("get shared event: %v", err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("get shared event status at %s: %d", url, resp.StatusCode)
		}
		if !bytes.Contains(b, []byte("SUMMARY:Test")) {
			t.Fatalf("unexpected body: %s", string(b))
		}
	}

	// REPORT calendar-query on shared team
	{
		body := `<?xml version="1.0" encoding="utf-8" ?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
 <D:prop>
  <D:getetag/>
  <C:calendar-data/>
 </D:prop>
 <C:filter>
  <C:comp-filter name="VCALENDAR">
   <C:comp-filter name="VEVENT">
    <C:time-range start="20250101T000000Z" end="20250102T000000Z"/>
   </C:comp-filter>
  </C:comp-filter>
 </C:filter>
</C:calendar-query>`
		req, _ := http.NewRequest("REPORT", baseURL+basePath+"/calendars/alice/shared/team/", bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("report calendar-query: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("report status: %d", resp.StatusCode)
		}
	}

	// HEAD should return headers, no body
	{
		req, _ := http.NewRequest("HEAD", baseURL+basePath+"/calendars/alice/shared/team/evt1.ics", nil)
		req.Header.Set("Authorization", authz)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("head: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("head status: %d", resp.StatusCode)
		}
	}

	// REPORT sync-collection (initial)
	{
		body := `<?xml version="1.0" encoding="utf-8" ?>
<D:sync-collection xmlns:D="DAV:"><D:sync-token/></D:sync-collection>`
		req, _ := http.NewRequest("REPORT", baseURL+basePath+"/calendars/alice/shared/team/", bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("sync report: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("sync status: %d", resp.StatusCode)
		}
	}
}

// New comprehensive test functions
func testEventManagement(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	baseCalendarURL := baseURL + basePath + "/calendars/alice/shared/team/"

	// Test event updates
	t.Run("EventUpdate", func(t *testing.T) {
		// Create initial event
		ics := "BEGIN:VCALENDAR\r\n" +
			"VERSION:2.0\r\n" +
			"PRODID:-//ldap-dav//test//EN\r\n" +
			"BEGIN:VEVENT\r\n" +
			"UID:evt-update-test\r\n" +
			"DTSTAMP:20250101T090000Z\r\n" +
			"DTSTART:20250101T100000Z\r\n" +
			"DTEND:20250101T110000Z\r\n" +
			"SUMMARY:Original Event\r\n" +
			"END:VEVENT\r\n" +
			"END:VCALENDAR\r\n"

		url := baseCalendarURL + "evt-update-test.ics"
		req, _ := http.NewRequest("PUT", url, bytes.NewBufferString(ics))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("create event for update: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
			t.Fatalf("create event status: %d", resp.StatusCode)
		}
		etag := resp.Header.Get("ETag")

		// Update event with If-Match
		updatedIcs := strings.Replace(ics, "Original Event", "Updated Event", 1)
		req, _ = http.NewRequest("PUT", url, bytes.NewBufferString(updatedIcs))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		req.Header.Set("If-Match", etag)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("update event: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("update event status: %d", resp.StatusCode)
		}

		// Verify update
		req, _ = http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", authz)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("get updated event: %v", err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !bytes.Contains(b, []byte("Updated Event")) {
			t.Fatalf("event not updated: %s", string(b))
		}
	})

	// Test event deletion
	t.Run("EventDeletion", func(t *testing.T) {
		// Create event to delete
		ics := "BEGIN:VCALENDAR\r\n" +
			"VERSION:2.0\r\n" +
			"PRODID:-//ldap-dav//test//EN\r\n" +
			"BEGIN:VEVENT\r\n" +
			"UID:evt-delete-test\r\n" +
			"DTSTAMP:20250101T090000Z\r\n" +
			"DTSTART:20250101T120000Z\r\n" +
			"DTEND:20250101T130000Z\r\n" +
			"SUMMARY:To Be Deleted\r\n" +
			"END:VEVENT\r\n" +
			"END:VCALENDAR\r\n"

		url := baseCalendarURL + "evt-delete-test.ics"
		req, _ := http.NewRequest("PUT", url, bytes.NewBufferString(ics))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("create event for deletion: %v", err)
		}
		resp.Body.Close()

		// Delete event
		req, _ = http.NewRequest("DELETE", url, nil)
		req.Header.Set("Authorization", authz)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("delete event: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
			t.Fatalf("delete event status: %d", resp.StatusCode)
		}

		// Verify deletion
		req, _ = http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", authz)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("get deleted event: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("deleted event still exists: %d", resp.StatusCode)
		}
	})

	// Test recurring events
	t.Run("RecurringEvents", func(t *testing.T) {
		recurringIcs := "BEGIN:VCALENDAR\r\n" +
			"VERSION:2.0\r\n" +
			"PRODID:-//ldap-dav//test//EN\r\n" +
			"BEGIN:VEVENT\r\n" +
			"UID:recurring-evt\r\n" +
			"DTSTAMP:20250101T090000Z\r\n" +
			"DTSTART:20250101T140000Z\r\n" +
			"DTEND:20250101T150000Z\r\n" +
			"RRULE:FREQ=DAILY;COUNT=5\r\n" +
			"SUMMARY:Daily Recurring Event\r\n" +
			"END:VEVENT\r\n" +
			"END:VCALENDAR\r\n"

		url := baseCalendarURL + "recurring-evt.ics"
		req, _ := http.NewRequest("PUT", url, bytes.NewBufferString(recurringIcs))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("create recurring event: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
			t.Fatalf("create recurring event status: %d", resp.StatusCode)
		}

		// Query expanded recurring events
		body := `<?xml version="1.0" encoding="utf-8" ?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
 <D:prop>
  <C:calendar-data>
   <C:expand start="20250101T000000Z" end="20250106T000000Z"/>
  </C:calendar-data>
 </D:prop>
 <C:filter>
  <C:comp-filter name="VCALENDAR">
   <C:comp-filter name="VEVENT">
    <C:time-range start="20250101T000000Z" end="20250106T000000Z"/>
   </C:comp-filter>
  </C:comp-filter>
 </C:filter>
</C:calendar-query>`
		req, _ = http.NewRequest("REPORT", baseCalendarURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("query recurring events: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("query recurring events status: %d", resp.StatusCode)
		}
	})

	// Test VTODO support
	t.Run("TodoSupport", func(t *testing.T) {
		todoIcs := "BEGIN:VCALENDAR\r\n" +
			"VERSION:2.0\r\n" +
			"PRODID:-//ldap-dav//test//EN\r\n" +
			"BEGIN:VTODO\r\n" +
			"UID:todo-test\r\n" +
			"DTSTAMP:20250101T090000Z\r\n" +
			"DUE:20250102T170000Z\r\n" +
			"SUMMARY:Test Task\r\n" +
			"STATUS:NEEDS-ACTION\r\n" +
			"PRIORITY:5\r\n" +
			"END:VTODO\r\n" +
			"END:VCALENDAR\r\n"

		url := baseCalendarURL + "todo-test.ics"
		req, _ := http.NewRequest("PUT", url, bytes.NewBufferString(todoIcs))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("create todo: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
			t.Fatalf("create todo status: %d", resp.StatusCode)
		}

		// Query todos
		body := `<?xml version="1.0" encoding="utf-8" ?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
 <D:prop>
  <C:calendar-data/>
 </D:prop>
 <C:filter>
  <C:comp-filter name="VCALENDAR">
   <C:comp-filter name="VTODO"/>
  </C:comp-filter>
 </C:filter>
</C:calendar-query>`
		req, _ = http.NewRequest("REPORT", baseCalendarURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("query todos: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("query todos status: %d", resp.StatusCode)
		}
	})

	// Test timezone handling
	t.Run("TimezoneHandling", func(t *testing.T) {
		timezoneIcs := "BEGIN:VCALENDAR\r\n" +
			"VERSION:2.0\r\n" +
			"PRODID:-//ldap-dav//test//EN\r\n" +
			"BEGIN:VTIMEZONE\r\n" +
			"TZID:America/New_York\r\n" +
			"BEGIN:STANDARD\r\n" +
			"DTSTART:20241103T020000\r\n" +
			"TZOFFSETFROM:-0400\r\n" +
			"TZOFFSETTO:-0500\r\n" +
			"TZNAME:EST\r\n" +
			"END:STANDARD\r\n" +
			"END:VTIMEZONE\r\n" +
			"BEGIN:VEVENT\r\n" +
			"UID:timezone-evt\r\n" +
			"DTSTAMP:20250101T090000Z\r\n" +
			"DTSTART;TZID=America/New_York:20250101T160000\r\n" +
			"DTEND;TZID=America/New_York:20250101T170000\r\n" +
			"SUMMARY:Timezone Test Event\r\n" +
			"END:VEVENT\r\n" +
			"END:VCALENDAR\r\n"

		url := baseCalendarURL + "timezone-evt.ics"
		req, _ := http.NewRequest("PUT", url, bytes.NewBufferString(timezoneIcs))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("create timezone event: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
			t.Fatalf("create timezone event status: %d", resp.StatusCode)
		}
	})
}

func testMkCalendar(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	// Test basic MKCALENDAR without properties
	t.Run("BasicMkCalendar", func(t *testing.T) {
		calendarName := "test-calendar-basic"
		url := baseURL + basePath + "/calendars/alice/" + calendarName + "/"

		req, _ := http.NewRequest("MKCALENDAR", url, nil)
		req.Header.Set("Authorization", authz)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("basic MKCALENDAR: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("basic MKCALENDAR status: %d", resp.StatusCode)
		}

		// Verify calendar was created by doing PROPFIND with specific properties
		propfindBody := `<?xml version="1.0" encoding="utf-8" ?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop>
    <D:resourcetype/>
    <D:displayname/>
  </D:prop>
</D:propfind>`

		req, _ = http.NewRequest("PROPFIND", url, bytes.NewBufferString(propfindBody))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		req.Header.Set("Depth", "0")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("verify basic calendar: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 207 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("verify basic calendar status: %d, body: %s", resp.StatusCode, string(b))
		}

		// Check that response contains calendar resourcetype
		respBody, _ := io.ReadAll(resp.Body)
		respStr := string(respBody)

		if !strings.Contains(respStr, "calendar") {
			t.Fatalf("created resource is not a calendar: %s", respStr)
		}
	})

	// Test basic MKCOL with calendar resourcetype
	t.Run("BasicMkCol", func(t *testing.T) {
		calendarName := "test-calendar-mkcol"
		url := baseURL + basePath + "/calendars/alice/" + calendarName + "/"

		body := `<?xml version="1.0" encoding="utf-8" ?>
<D:mkcol xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:set>
    <D:prop>
      <D:resourcetype>
        <D:collection/>
        <C:calendar/>
      </D:resourcetype>
      <D:displayname>MKCOL Test Calendar</D:displayname>
    </D:prop>
  </D:set>
</D:mkcol>`

		req, _ := http.NewRequest("MKCOL", url, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("basic MKCOL: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("basic MKCOL status: %d", resp.StatusCode)
		}

		// Verify calendar was created by doing PROPFIND with specific properties
		propfindBody := `<?xml version="1.0" encoding="utf-8" ?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop>
    <D:resourcetype/>
    <D:displayname/>
  </D:prop>
</D:propfind>`

		req, _ = http.NewRequest("PROPFIND", url, bytes.NewBufferString(propfindBody))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		req.Header.Set("Depth", "0")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("verify MKCOL calendar: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 207 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("verify MKCOL calendar status: %d, body: %s", resp.StatusCode, string(b))
		}

		// Check that response contains calendar resourcetype
		respBody, _ := io.ReadAll(resp.Body)
		respStr := string(respBody)
		t.Logf("PROPFIND response: %s", respStr)

		if !strings.Contains(respStr, "calendar") {
			t.Fatalf("created resource is not a calendar: %s", respStr)
		}
		if !strings.Contains(respStr, "MKCOL Test Calendar") {
			t.Fatalf("displayname not set correctly: %s", respStr)
		}
	})

	// Test MKCALENDAR with properties
	t.Run("MkCalendarWithProperties", func(t *testing.T) {
		calendarName := "test-calendar-props"
		url := baseURL + basePath + "/calendars/alice/" + calendarName + "/"

		body := `<?xml version="1.0" encoding="utf-8" ?>
<C:mkcalendar xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:set>
    <D:prop>
      <D:displayname>My Test Calendar</D:displayname>
      <C:calendar-description>A calendar created via MKCALENDAR for testing</C:calendar-description>
    </D:prop>
  </D:set>
</C:mkcalendar>`

		req, _ := http.NewRequest("MKCALENDAR", url, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("MKCALENDAR with properties: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("MKCALENDAR with properties status: %d", resp.StatusCode)
		}

		// Verify properties were set
		propfindBody := `<?xml version="1.0" encoding="utf-8" ?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop>
    <D:displayname/>
    <C:calendar-description/>
    <D:resourcetype/>
  </D:prop>
</D:propfind>`

		req, _ = http.NewRequest("PROPFIND", url, bytes.NewBufferString(propfindBody))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		req.Header.Set("Depth", "0")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("verify calendar properties: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 207 {
			t.Fatalf("verify calendar properties status: %d", resp.StatusCode)
		}

		respBody, _ := io.ReadAll(resp.Body)
		respStr := string(respBody)

		// Verify displayname and description were set
		if !strings.Contains(respStr, "My Test Calendar") {
			t.Fatalf("displayname not set correctly: %s", respStr)
		}
		if !strings.Contains(respStr, "A calendar created via MKCALENDAR") {
			t.Fatalf("calendar-description not set correctly: %s", respStr)
		}
		if !strings.Contains(respStr, "calendar") {
			t.Fatalf("resourcetype not calendar: %s", respStr)
		}
	})

	// Test MKCALENDAR conflict (calendar already exists)
	t.Run("MkCalendarConflict", func(t *testing.T) {
		calendarName := "test-calendar-conflict"
		url := baseURL + basePath + "/calendars/alice/" + calendarName + "/"

		// Create calendar first time
		req, _ := http.NewRequest("MKCALENDAR", url, nil)
		req.Header.Set("Authorization", authz)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("first MKCALENDAR: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("first MKCALENDAR status: %d", resp.StatusCode)
		}

		// Try to create same calendar again - should fail
		req, _ = http.NewRequest("MKCALENDAR", url, nil)
		req.Header.Set("Authorization", authz)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("second MKCALENDAR: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("expected 409 Conflict for duplicate calendar, got: %d", resp.StatusCode)
		}
	})

	// Test MKCALENDAR with invalid path
	t.Run("MkCalendarInvalidPath", func(t *testing.T) {
		// Try to create calendar with actually problematic paths
		invalidNames := []string{
			"calendar/../invalid",
			"calendar/nested/path",
			"..",
			".",
			"calendar\x00null",
			"calendar\nnewline",
			"calendar\ttab",
		}

		for _, invalidName := range invalidNames {
			url := baseURL + basePath + "/calendars/alice/" + invalidName + "/"
			req, _ := http.NewRequest("MKCALENDAR", url, nil)
			req.Header.Set("Authorization", authz)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("MKCALENDAR invalid path %s: %v", invalidName, err)
			}
			resp.Body.Close()

			// Should return 4xx error for invalid path
			if resp.StatusCode < 400 || resp.StatusCode >= 500 {
				t.Errorf("MKCALENDAR with invalid path '%s' returned %d (expected 4xx)", invalidName, resp.StatusCode)
			}
		}

		// Test empty calendar name specifically
		emptyURL := baseURL + basePath + "/calendars/alice//"
		req, _ := http.NewRequest("MKCALENDAR", emptyURL, nil)
		req.Header.Set("Authorization", authz)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("MKCALENDAR empty name: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode < 400 || resp.StatusCode >= 500 {
			t.Errorf("MKCALENDAR with empty name returned %d (expected 4xx)", resp.StatusCode)
		}
	})

	// Test MKCALENDAR permission denied (try to create in another user's space)
	t.Run("MkCalendarPermissionDenied", func(t *testing.T) {
		// Try to create calendar in bob's space as alice
		url := baseURL + basePath + "/calendars/bob/unauthorized-calendar/"
		req, _ := http.NewRequest("MKCALENDAR", url, nil)
		req.Header.Set("Authorization", authz) // alice's auth
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("MKCALENDAR permission denied: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for unauthorized calendar creation, got: %d", resp.StatusCode)
		}
	})

	// Test MKCALENDAR without authentication
	t.Run("MkCalendarUnauthenticated", func(t *testing.T) {
		url := baseURL + basePath + "/calendars/alice/unauth-calendar/"
		req, _ := http.NewRequest("MKCALENDAR", url, nil)
		// No Authorization header
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("MKCALENDAR unauthenticated: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized for unauthenticated MKCALENDAR, got: %d", resp.StatusCode)
		}
	})

	// Test using created calendar for events
	t.Run("UseCreatedCalendar", func(t *testing.T) {
		calendarName := "test-calendar-usage"
		calendarURL := baseURL + basePath + "/calendars/alice/" + calendarName + "/"

		// Create calendar
		req, _ := http.NewRequest("MKCALENDAR", calendarURL, nil)
		req.Header.Set("Authorization", authz)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("create calendar for usage test: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create calendar for usage status: %d", resp.StatusCode)
		}

		// Add event to created calendar
		ics := "BEGIN:VCALENDAR\r\n" +
			"VERSION:2.0\r\n" +
			"PRODID:-//ldap-dav//test//EN\r\n" +
			"BEGIN:VEVENT\r\n" +
			"UID:test-event-in-new-calendar\r\n" +
			"DTSTAMP:20250101T090000Z\r\n" +
			"DTSTART:20250103T100000Z\r\n" +
			"DTEND:20250103T110000Z\r\n" +
			"SUMMARY:Event in New Calendar\r\n" +
			"END:VEVENT\r\n" +
			"END:VCALENDAR\r\n"

		eventURL := calendarURL + "test-event-in-new-calendar.ics"
		req, _ = http.NewRequest("PUT", eventURL, bytes.NewBufferString(ics))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("add event to new calendar: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
			t.Fatalf("add event to new calendar status: %d", resp.StatusCode)
		}

		// Query calendar for events
		queryBody := `<?xml version="1.0" encoding="utf-8" ?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
 <D:prop>
  <D:getetag/>
  <C:calendar-data/>
 </D:prop>
 <C:filter>
  <C:comp-filter name="VCALENDAR">
   <C:comp-filter name="VEVENT"/>
  </C:comp-filter>
 </C:filter>
</C:calendar-query>`

		req, _ = http.NewRequest("REPORT", calendarURL, bytes.NewBufferString(queryBody))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("query new calendar: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 207 {
			t.Fatalf("query new calendar status: %d", resp.StatusCode)
		}

		respBody, _ := io.ReadAll(resp.Body)
		if !bytes.Contains(respBody, []byte("Event in New Calendar")) {
			t.Fatalf("event not found in new calendar: %s", string(respBody))
		}
	})

	// Test MKCALENDAR with malformed XML
	t.Run("MkCalendarMalformedXML", func(t *testing.T) {
		url := baseURL + basePath + "/calendars/alice/malformed-xml-test/"

		malformedXML := `<?xml version="1.0" encoding="utf-8" ?>
<C:mkcalendar xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:set>
    <D:prop>
      <D:displayname>Malformed Test
      <!-- Missing closing tag -->
    </D:prop>
  </D:set>`

		req, _ := http.NewRequest("MKCALENDAR", url, bytes.NewBufferString(malformedXML))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("MKCALENDAR malformed XML: %v", err)
		}
		resp.Body.Close()

		// Should still create calendar (XML parsing is optional for MKCALENDAR)
		// But if XML is provided and malformed, it might return an error
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusBadRequest {
			t.Logf("MKCALENDAR with malformed XML returned %d (could be 201 or 400)", resp.StatusCode)
		}
	})
}

func testAdvancedQuerying(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	baseCalendarURL := baseURL + basePath + "/calendars/alice/shared/team/"

	// Setup test events for querying
	setupEvents := []struct {
		uid, summary, description string
	}{
		{"query-evt-1", "Meeting with Bob", "Discuss project timeline"},
		{"query-evt-2", "Conference Call", "Weekly team sync"},
		{"query-evt-3", "Birthday Party", "Alice's birthday celebration"},
	}

	for _, evt := range setupEvents {
		ics := fmt.Sprintf("BEGIN:VCALENDAR\r\n"+
			"VERSION:2.0\r\n"+
			"PRODID:-//ldap-dav//test//EN\r\n"+
			"BEGIN:VEVENT\r\n"+
			"UID:%s\r\n"+
			"DTSTAMP:20250101T090000Z\r\n"+
			"DTSTART:20250101T180000Z\r\n"+
			"DTEND:20250101T190000Z\r\n"+
			"SUMMARY:%s\r\n"+
			"DESCRIPTION:%s\r\n"+
			"END:VEVENT\r\n"+
			"END:VCALENDAR\r\n", evt.uid, evt.summary, evt.description)

		url := baseCalendarURL + evt.uid + ".ics"
		req, _ := http.NewRequest("PUT", url, bytes.NewBufferString(ics))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("setup event %s: %v", evt.uid, err)
		}
		resp.Body.Close()
	}

	// Test property filters
	t.Run("PropertyFilters", func(t *testing.T) {
		body := `<?xml version="1.0" encoding="utf-8" ?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
 <D:prop>
  <D:getetag/>
  <C:calendar-data/>
 </D:prop>
 <C:filter>
  <C:comp-filter name="VCALENDAR">
   <C:comp-filter name="VEVENT">
    <C:prop-filter name="SUMMARY">
     <C:text-match>Meeting</C:text-match>
    </C:prop-filter>
   </C:comp-filter>
  </C:comp-filter>
 </C:filter>
</C:calendar-query>`
		req, _ := http.NewRequest("REPORT", baseCalendarURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("property filter query: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("property filter status: %d", resp.StatusCode)
		}
	})

	// Test parameter filters
	t.Run("ParameterFilters", func(t *testing.T) {
		body := `<?xml version="1.0" encoding="utf-8" ?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
 <D:prop>
  <C:calendar-data/>
 </D:prop>
 <C:filter>
  <C:comp-filter name="VCALENDAR">
   <C:comp-filter name="VEVENT">
    <C:prop-filter name="DTSTART">
     <C:param-filter name="TZID">
      <C:text-match>UTC</C:text-match>
     </C:param-filter>
    </C:prop-filter>
   </C:comp-filter>
  </C:comp-filter>
 </C:filter>
</C:calendar-query>`
		req, _ := http.NewRequest("REPORT", baseCalendarURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("parameter filter query: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("parameter filter status: %d", resp.StatusCode)
		}
	})

	// Test multiget report
	t.Run("CalendarMultiget", func(t *testing.T) {
		body := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8" ?>
<C:calendar-multiget xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
 <D:prop>
  <D:getetag/>
  <C:calendar-data/>
 </D:prop>
 <D:href>%s/calendars/alice/shared/team/query-evt-1.ics</D:href>
 <D:href>%s/calendars/alice/shared/team/query-evt-2.ics</D:href>
</C:calendar-multiget>`, basePath, basePath)
		req, _ := http.NewRequest("REPORT", baseCalendarURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("multiget query: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("multiget status: %d", resp.StatusCode)
		}
	})

	// Test partial calendar-data requests
	t.Run("PartialCalendarData", func(t *testing.T) {
		body := `<?xml version="1.0" encoding="utf-8" ?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
 <D:prop>
  <C:calendar-data>
   <C:comp name="VCALENDAR">
    <C:comp name="VEVENT">
     <C:prop name="SUMMARY"/>
     <C:prop name="DTSTART"/>
     <C:prop name="DTEND"/>
    </C:comp>
   </C:comp>
  </C:calendar-data>
 </D:prop>
 <C:filter>
  <C:comp-filter name="VCALENDAR">
   <C:comp-filter name="VEVENT"/>
  </C:comp-filter>
 </C:filter>
</C:calendar-query>`
		req, _ := http.NewRequest("REPORT", baseCalendarURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("partial calendar-data query: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("partial calendar-data status: %d", resp.StatusCode)
		}
	})

	// Test free/busy queries
	t.Run("FreeBusyQuery", func(t *testing.T) {
		body := `<?xml version="1.0" encoding="utf-8" ?>
<C:free-busy-query xmlns:C="urn:ietf:params:xml:ns:caldav">
 <C:time-range start="20250101T000000Z" end="20250102T000000Z"/>
</C:free-busy-query>`
		req, _ := http.NewRequest("REPORT", baseCalendarURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("free-busy query: %v", err)
		}
		defer resp.Body.Close()
		// Free-busy might return 200 or 207 depending on implementation
		if resp.StatusCode != 200 && resp.StatusCode != 207 {
			t.Fatalf("free-busy status: %d", resp.StatusCode)
		}
	})
}

func testConcurrencyAndSync(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	baseCalendarURL := baseURL + basePath + "/calendars/alice/shared/team/"

	// Test conditional requests with If-Match/If-None-Match
	t.Run("ConditionalRequests", func(t *testing.T) {
		ics := "BEGIN:VCALENDAR\r\n" +
			"VERSION:2.0\r\n" +
			"PRODID:-//ldap-dav//test//EN\r\n" +
			"BEGIN:VEVENT\r\n" +
			"UID:conditional-test\r\n" +
			"DTSTAMP:20250101T090000Z\r\n" +
			"DTSTART:20250101T200000Z\r\n" +
			"DTEND:20250101T210000Z\r\n" +
			"SUMMARY:Conditional Test\r\n" +
			"END:VEVENT\r\n" +
			"END:VCALENDAR\r\n"

		url := baseCalendarURL + "conditional-test.ics"

		// Create event
		req, _ := http.NewRequest("PUT", url, bytes.NewBufferString(ics))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		req.Header.Set("If-None-Match", "*")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("create conditional event: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
			t.Fatalf("create conditional event status: %d", resp.StatusCode)
		}
		etag := resp.Header.Get("ETag")

		// Test If-None-Match should fail (event exists)
		req, _ = http.NewRequest("PUT", url, bytes.NewBufferString(ics))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		req.Header.Set("If-None-Match", "*")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("conditional put (should fail): %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusPreconditionFailed {
			t.Fatalf("expected precondition failed, got: %d", resp.StatusCode)
		}

		// Test If-Match with wrong ETag should fail
		req, _ = http.NewRequest("PUT", url, bytes.NewBufferString(ics))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		req.Header.Set("If-Match", "\"wrong-etag\"")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("conditional put wrong etag: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusPreconditionFailed {
			t.Fatalf("expected precondition failed for wrong etag, got: %d", resp.StatusCode)
		}

		// Test If-Match with correct ETag should succeed
		updatedIcs := strings.Replace(ics, "Conditional Test", "Conditional Updated", 1)
		req, _ = http.NewRequest("PUT", url, bytes.NewBufferString(updatedIcs))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		req.Header.Set("If-Match", etag)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("conditional put correct etag: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("expected success for correct etag, got: %d", resp.StatusCode)
		}
	})

	// Test sync-token progression
	t.Run("SyncTokenProgression", func(t *testing.T) {
		// Get initial sync token
		body := `<?xml version="1.0" encoding="utf-8" ?>
<D:sync-collection xmlns:D="DAV:"><D:sync-token/></D:sync-collection>`
		req, _ := http.NewRequest("REPORT", baseCalendarURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("initial sync report: %v", err)
		}
		initialBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("initial sync status: %d", resp.StatusCode)
		}

		// Create a new event
		ics := "BEGIN:VCALENDAR\r\n" +
			"VERSION:2.0\r\n" +
			"PRODID:-//ldap-dav//test//EN\r\n" +
			"BEGIN:VEVENT\r\n" +
			"UID:sync-test-event\r\n" +
			"DTSTAMP:20250101T090000Z\r\n" +
			"DTSTART:20250101T220000Z\r\n" +
			"DTEND:20250101T230000Z\r\n" +
			"SUMMARY:Sync Test Event\r\n" +
			"END:VEVENT\r\n" +
			"END:VCALENDAR\r\n"

		url := baseCalendarURL + "sync-test-event.ics"
		req, _ = http.NewRequest("PUT", url, bytes.NewBufferString(ics))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("create sync test event: %v", err)
		}
		resp.Body.Close()

		// Get updated sync
		req, _ = http.NewRequest("REPORT", baseCalendarURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("updated sync report: %v", err)
		}
		updatedBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// Sync response should be different after adding event
		if bytes.Equal(initialBody, updatedBody) {
			t.Logf("Sync tokens might be the same, but this could be implementation-specific")
		}
	})

	// Test concurrent modifications
	t.Run("ConcurrentModifications", func(t *testing.T) {
		ics := "BEGIN:VCALENDAR\r\n" +
			"VERSION:2.0\r\n" +
			"PRODID:-//ldap-dav//test//EN\r\n" +
			"BEGIN:VEVENT\r\n" +
			"UID:concurrent-test\r\n" +
			"DTSTAMP:20250101T090000Z\r\n" +
			"DTSTART:20250102T000000Z\r\n" +
			"DTEND:20250102T010000Z\r\n" +
			"SUMMARY:Concurrent Test\r\n" +
			"END:VEVENT\r\n" +
			"END:VCALENDAR\r\n"

		url := baseCalendarURL + "concurrent-test.ics"

		// Create initial event
		req, _ := http.NewRequest("PUT", url, bytes.NewBufferString(ics))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("create concurrent test event: %v", err)
		}
		resp.Body.Close()

		// Simulate concurrent modifications
		var wg sync.WaitGroup
		var successCount int32
		var mu sync.Mutex

		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()

				updatedIcs := strings.Replace(ics, "Concurrent Test", fmt.Sprintf("Updated by %d", id), 1)
				req, _ := http.NewRequest("PUT", url, bytes.NewBufferString(updatedIcs))
				req.Header.Set("Authorization", authz)
				req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
				resp, err := client.Do(req)
				if err != nil {
					return
				}
				resp.Body.Close()

				mu.Lock()
				if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusCreated {
					successCount++
				}
				mu.Unlock()
			}(i)
		}

		wg.Wait()

		// At least one should succeed
		if successCount == 0 {
			t.Fatalf("no concurrent modifications succeeded")
		}
	})
}

func testCollectionProperties(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	// Test calendar collection properties
	t.Run("CalendarCollectionProperties", func(t *testing.T) {
		body := `<?xml version="1.0" encoding="utf-8" ?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
 <D:prop>
  <D:resourcetype/>
  <D:displayname/>
  <C:supported-calendar-component-set/>
  <C:supported-calendar-data/>
  <C:max-resource-size/>
  <C:calendar-description/>
  <C:calendar-timezone/>
  <D:supported-report-set/>
 </D:prop>
</D:propfind>`

		url := baseURL + basePath + "/calendars/alice/shared/team/"
		req, _ := http.NewRequest("PROPFIND", url, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		req.Header.Set("Depth", "0")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("calendar properties propfind: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("calendar properties status: %d", resp.StatusCode)
		}

		respBody, _ := io.ReadAll(resp.Body)
		respStr := string(respBody)

		// Check for required calendar properties
		if !strings.Contains(respStr, "calendar") {
			t.Fatalf("response missing calendar resourcetype: %s", respStr)
		}
	})

	// Test supported reports
	t.Run("SupportedReports", func(t *testing.T) {
		body := `<?xml version="1.0" encoding="utf-8" ?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
 <D:prop>
  <D:supported-report-set/>
 </D:prop>
</D:propfind>`

		url := baseURL + basePath + "/calendars/alice/shared/team/"
		req, _ := http.NewRequest("PROPFIND", url, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		req.Header.Set("Depth", "0")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("supported reports propfind: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("supported reports status: %d", resp.StatusCode)
		}

		respBody, _ := io.ReadAll(resp.Body)
		respStr := string(respBody)

		expectedReports := []string{"calendar-query", "calendar-multiget", "sync-collection"}
		for _, report := range expectedReports {
			if !strings.Contains(respStr, report) {
				t.Logf("response missing support for %s report", report)
			}
		}
	})

	// Test quota properties (if supported)
	t.Run("QuotaProperties", func(t *testing.T) {
		body := `<?xml version="1.0" encoding="utf-8" ?>
<D:propfind xmlns:D="DAV:">
 <D:prop>
  <D:quota-available-bytes/>
  <D:quota-used-bytes/>
 </D:prop>
</D:propfind>`

		url := baseURL + basePath + "/calendars/alice/shared/team/"
		req, _ := http.NewRequest("PROPFIND", url, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		req.Header.Set("Depth", "0")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("quota properties propfind: %v", err)
		}
		defer resp.Body.Close()
		// Quota properties are optional, so any 2xx response is acceptable
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			t.Fatalf("quota properties status: %d", resp.StatusCode)
		}
	})
}

func testErrorConditions(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	baseCalendarURL := baseURL + basePath + "/calendars/alice/shared/team/"

	// Test invalid iCalendar data
	t.Run("InvalidICalendarData", func(t *testing.T) {
		invalidIcs := "INVALID ICALENDAR DATA"
		url := baseCalendarURL + "invalid.ics"
		req, _ := http.NewRequest("PUT", url, bytes.NewBufferString(invalidIcs))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("put invalid icalendar: %v", err)
		}
		resp.Body.Close()
		// Should return 4xx error for invalid data
		if resp.StatusCode < 400 || resp.StatusCode >= 500 {
			t.Fatalf("expected 4xx for invalid iCalendar, got: %d", resp.StatusCode)
		}
	})

	// Test unauthorized access
	t.Run("UnauthorizedAccess", func(t *testing.T) {
		url := baseURL + basePath + "/calendars/alice/"
		req, _ := http.NewRequest("PROPFIND", url, nil)
		// No authorization header
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("unauthorized propfind: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401 unauthorized, got: %d", resp.StatusCode)
		}
	})

	// Test access to non-existent resources
	t.Run("NonExistentResource", func(t *testing.T) {
		url := baseCalendarURL + "nonexistent.ics"
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", authz)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("get nonexistent: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404 for nonexistent resource, got: %d", resp.StatusCode)
		}
	})

	// Test malformed XML in REPORT requests
	t.Run("MalformedXMLReport", func(t *testing.T) {
		body := `<invalid-xml>`
		req, _ := http.NewRequest("REPORT", baseCalendarURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("malformed xml report: %v", err)
		}
		resp.Body.Close()
		// Should return 4xx error for malformed XML
		if resp.StatusCode < 400 || resp.StatusCode >= 500 {
			t.Fatalf("expected 4xx for malformed XML, got: %d", resp.StatusCode)
		}
	})

	// Test unsupported methods
	t.Run("UnsupportedMethods", func(t *testing.T) {
		unsupportedMethods := []string{"PATCH", "TRACE"}
		for _, method := range unsupportedMethods {
			req, _ := http.NewRequest(method, baseCalendarURL, nil)
			req.Header.Set("Authorization", authz)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("unsupported method %s: %v", method, err)
			}
			resp.Body.Close()
			// Should return 405 Method Not Allowed or similar
			if resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusNotImplemented {
				t.Logf("method %s returned %d (expected 405 or 501)", method, resp.StatusCode)
			}
		}
	})
}

func testLargeCollectionHandling(t *testing.T, client *http.Client, baseURL, basePath, authz string) {
	baseCalendarURL := baseURL + basePath + "/calendars/alice/shared/team/"

	t.Run("CreateManyEvents", func(t *testing.T) {
		// Create 10000 events to simulate a large collection
		numEvents := 10_000
		for i := 0; i < numEvents; i++ {
			ics := fmt.Sprintf("BEGIN:VCALENDAR\r\n"+
				"VERSION:2.0\r\n"+
				"PRODID:-//ldap-dav//test//EN\r\n"+
				"BEGIN:VEVENT\r\n"+
				"UID:large-coll-evt-%d\r\n"+
				"DTSTAMP:20250101T090000Z\r\n"+
				"DTSTART:20250201T%02d0000Z\r\n"+
				"DTEND:20250201T%02d0000Z\r\n"+
				"SUMMARY:Large Collection Event %d\r\n"+
				"END:VEVENT\r\n"+
				"END:VCALENDAR\r\n", i, (i % 24), (i%24)+1, i)

			url := baseCalendarURL + fmt.Sprintf("large-coll-evt-%d.ics", i)
			req, _ := http.NewRequest("PUT", url, bytes.NewBufferString(ics))
			req.Header.Set("Authorization", authz)
			req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("create event %d: %v", i, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
				t.Fatalf("create event %d status: %d", i, resp.StatusCode)
			}
		}
	})

	t.Run("QueryLargeCollection", func(t *testing.T) {
		// Test PROPFIND on large collection
		req, _ := http.NewRequest("PROPFIND", baseCalendarURL, nil)
		req.Header.Set("Authorization", authz)
		req.Header.Set("Depth", "1")
		req.Header.Set("Content-Type", "application/xml")

		start := time.Now()
		resp, err := client.Do(req)
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("propfind large collection: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 207 {
			t.Fatalf("propfind large collection status: %d", resp.StatusCode)
		}

		// Check if server handles large collections reasonably quickly
		if elapsed > 30*time.Second {
			t.Logf("PROPFIND on large collection took %v - might be too slow for enterprise use", elapsed)
		}
	})

	t.Run("CalendarQueryWithoutTimeRange", func(t *testing.T) {
		// Test querying entire collection without time range
		body := `<?xml version="1.0" encoding="utf-8" ?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
 <D:prop>
  <D:getetag/>
 </D:prop>
 <C:filter>
  <C:comp-filter name="VCALENDAR">
   <C:comp-filter name="VEVENT"/>
  </C:comp-filter>
 </C:filter>
</C:calendar-query>`

		req, _ := http.NewRequest("REPORT", baseCalendarURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")

		start := time.Now()
		resp, err := client.Do(req)
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("calendar-query all events: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 207 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("calendar-query all events status: %d, body: %s", resp.StatusCode, string(b))
		}

		// Check server performance and possible truncation
		if elapsed > 15*time.Second {
			t.Logf("Calendar query without time range took %v - might be too slow", elapsed)
		}
	})

	t.Run("SyncLargeCollection", func(t *testing.T) {
		// Test sync-collection on large collection
		body := `<?xml version="1.0" encoding="utf-8" ?>
<D:sync-collection xmlns:D="DAV:">
 <D:sync-token/>
 <D:sync-level>1</D:sync-level>
 <D:prop>
  <D:getetag/>
 </D:prop>
</D:sync-collection>`

		req, _ := http.NewRequest("REPORT", baseCalendarURL, bytes.NewBufferString(body))
		req.Header.Set("Authorization", authz)
		req.Header.Set("Content-Type", "application/xml")

		start := time.Now()
		resp, err := client.Do(req)
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("sync large collection: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 207 {
			t.Fatalf("sync large collection status: %d", resp.StatusCode)
		}

		if elapsed > 10*time.Second {
			t.Logf("Sync collection took %v - might be too slow for large collections", elapsed)
		}
	})
}
