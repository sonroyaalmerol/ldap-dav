package caldav

import "net/http"

type headResponseWriter struct {
	http.ResponseWriter
}

func (hrw *headResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
