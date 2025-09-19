package integration

import (
	"bytes"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
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

	// .well-known redirect
	{
		req, _ := http.NewRequest("GET", baseURL+"/.well-known/caldav", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("well-known: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusPermanentRedirect && resp.StatusCode != http.StatusMovedPermanently {
			t.Fatalf("well-known status: %d", resp.StatusCode)
		}
	}

	// OPTIONS
	{
		req, _ := http.NewRequest("OPTIONS", baseURL+basePath+"/", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("options: %v", err)
		}
		_ = resp.Body.Close()
		got := resp.Header.Get("DAV")
		if got == "" || !bytes.Contains([]byte(got), []byte("calendar-access")) {
			t.Fatalf("DAV header missing calendar-access: %q", got)
		}
	}

	// Basic auth principal/home
	authz := basicAuth("alice", "password")
	{
		req, _ := http.NewRequest("PROPFIND", baseURL+basePath+"/principals/users/alice", nil)
		req.Header.Set("Authorization", authz)
		req.Header.Set("Depth", "0")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("propfind principal: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			t.Fatalf("propfind principal status: %d", resp.StatusCode)
		}
	}

	// Calendar home listing
	{
		req, _ := http.NewRequest("PROPFIND", baseURL+basePath+"/calendars/alice/", nil)
		req.Header.Set("Authorization", authz)
		req.Header.Set("Depth", "1")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("propfind home: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 207 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("propfind home status: %d body=%s", resp.StatusCode, string(b))
		}
	}

	// PUT event into bob/team as alice (should be allowed via editors group)
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:evt1\r\nDTSTART:20250101T100000Z\r\nDTEND:20250101T110000Z\r\nSUMMARY:Test\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	{
		req, _ := http.NewRequest("PUT", baseURL+basePath+"/calendars/alice/shared/team/evt1.ics", bytes.NewBufferString(ics))
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
			t.Fatalf("put shared event status: %d body=%s", resp.StatusCode, string(b))
		}
		etag := resp.Header.Get("ETag")
		if etag == "" {
			t.Fatalf("missing ETag on PUT")
		}
	}

	// GET event via shared path
	{
		req, _ := http.NewRequest("GET", baseURL+basePath+"/calendars/alice/shared/team/evt1.ics", nil)
		req.Header.Set("Authorization", authz)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("get shared event: %v", err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("get shared event status: %d", resp.StatusCode)
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
