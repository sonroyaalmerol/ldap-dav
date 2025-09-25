package router

import (
	"net"
	"net/http"
	"strings"
)

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	bytes       int
}

func (rec *statusRecorder) WriteHeader(code int) {
	if !rec.wroteHeader {
		rec.status = code
		rec.wroteHeader = true
	}
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Write(data []byte) (int, error) {
	if !rec.wroteHeader {
		rec.WriteHeader(http.StatusOK)
	}
	n, err := rec.ResponseWriter.Write(data)
	rec.bytes += n
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

func statusOrDefault(st int) int {
	if st == 0 {
		return http.StatusOK
	}
	return st
}
