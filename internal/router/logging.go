package router

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

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
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

func realIP(req *http.Request) string {
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

func loggingMiddleware(logger zerolog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 0, wroteHeader: false}

		ip := realIP(req)
		method := req.Method
		path := req.URL.Path
		ua := req.Header.Get("User-Agent")

		next.ServeHTTP(rec, req)

		dur := time.Since(start)

		logger.Info().
			Str("method", method).
			Str("path", path).
			Int("status", statusOrDefault(rec.status)).
			Int("bytes", rec.bytes).
			Float64("duration_ms", float64(dur.Microseconds())/1000.0).
			Str("ip", ip).
			Str("user_agent", ua).
			Msg("http request")
	})
}

func statusOrDefault(st int) int {
	if st == 0 {
		return http.StatusOK
	}
	return st
}
