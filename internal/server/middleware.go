package server

import (
	"bufio"
	"errors"
	"net"
	"net/http"
)

// securityResponseWriter wraps http.ResponseWriter to inject security headers
// while preserving http.Flusher, http.Hijacker, and http.ResponseController Unwrap support.
type securityResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *securityResponseWriter) setSecurityHeaders() {
	h := w.ResponseWriter.Header()
	if h.Get("Strict-Transport-Security") == "" {
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	}
	if h.Get("X-Frame-Options") == "" {
		h.Set("X-Frame-Options", "DENY")
	}
	if h.Get("X-Content-Type-Options") == "" {
		h.Set("X-Content-Type-Options", "nosniff")
	}
	if h.Get("Referrer-Policy") == "" {
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	}
}

func (w *securityResponseWriter) WriteHeader(statusCode int) {
	if !w.wroteHeader {
		w.setSecurityHeaders()
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *securityResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.setSecurityHeaders()
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

// Flush implements http.Flusher for streaming responses such as SSE.
func (w *securityResponseWriter) Flush() {
	if !w.wroteHeader {
		w.setSecurityHeaders()
		w.wroteHeader = true
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack implements http.Hijacker for protocol upgrades such as WebSockets.
func (w *securityResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, errors.New("http.Hijacker not supported by underlying ResponseWriter")
}

// Unwrap implements the interface required by http.ResponseController (Go 1.20+).
func (w *securityResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// SecurityHeadersMiddleware injects standard edge security headers:
// - Strict-Transport-Security: max-age=31536000; includeSubDomains
// - X-Frame-Options: DENY
// - X-Content-Type-Options: nosniff
// - Referrer-Policy: strict-origin-when-cross-origin
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapped := &securityResponseWriter{ResponseWriter: w}
		wrapped.setSecurityHeaders()
		next.ServeHTTP(wrapped, r)
	})
}
