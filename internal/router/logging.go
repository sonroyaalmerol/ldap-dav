package router

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// statusRecorder captures status and size
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	bytes       int
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
		r.ResponseWriter.WriteHeader(code)
	}
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		// Default status if Write called without explicit header
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

// realIP extracts the best-guess client IP
func realIP(req *http.Request) string {
	// If you’re behind a reverse proxy and trust it, consider X-Forwarded-For / X-Real-IP
	xff := req.Header.Get("X-Forwarded-For")
	if xff != "" {
		parts := strings.Split(xff, ",")
		ip := strings.TrimSpace(parts[0])
		if ip != "" {
			return ip
		}
	}
	if xr := req.Header.Get("X-Real-IP"); xr != "" {
		return xr
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

// loggingMiddleware wraps an http.Handler and logs request + response data
func loggingMiddleware(logger zerolog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 0, wroteHeader: false}

		// Pull some context that might be set later (principal after auth) is not yet available here.
		ip := realIP(req)
		method := req.Method
		path := req.URL.Path
		ua := req.Header.Get("User-Agent")

		// Proceed to next
		next.ServeHTTP(rec, req)

		dur := time.Since(start)

		// If you have a structured logger, replace this with fields-based logging
		logger.Printf(
			`access method=%q path=%q status=%d bytes=%d duration_ms=%.3f ip=%q ua=%q`,
			method, path, statusOrDefault(rec.status), rec.bytes, float64(dur.Microseconds())/1000.0, ip, ua,
		)
	})
}

func statusOrDefault(st int) int {
	if st == 0 {
		return http.StatusOK
	}
	return st
}
