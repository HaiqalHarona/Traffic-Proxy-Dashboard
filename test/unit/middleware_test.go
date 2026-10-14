package unit_test

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/server"
)

type mockHijackableFlusher struct {
	http.ResponseWriter
	flushed  bool
	hijacked bool
}

func (m *mockHijackableFlusher) Flush() {
	m.flushed = true
}

func (m *mockHijackableFlusher) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	m.hijacked = true
	return nil, nil, nil
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	t.Parallel()

	handler := server.SecurityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello world"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()

	if h := resp.Header.Get("Strict-Transport-Security"); h != "max-age=31536000; includeSubDomains" {
		t.Errorf("Strict-Transport-Security = %q; want %q", h, "max-age=31536000; includeSubDomains")
	}
	if h := resp.Header.Get("X-Frame-Options"); h != "DENY" {
		t.Errorf("X-Frame-Options = %q; want %q", h, "DENY")
	}
	if h := resp.Header.Get("X-Content-Type-Options"); h != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q; want %q", h, "nosniff")
	}
	if h := resp.Header.Get("Referrer-Policy"); h != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy = %q; want %q", h, "strict-origin-when-cross-origin")
	}
}

func TestSecurityHeadersMiddleware_FlusherAndHijackerPreservation(t *testing.T) {
	t.Parallel()

	mock := &mockHijackableFlusher{
		ResponseWriter: httptest.NewRecorder(),
	}

	var flushedCalled bool
	var hijackedCalled bool
	var unwrapSucceeded bool

	handler := server.SecurityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify http.Flusher
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
			flushedCalled = true
		}

		// Verify http.Hijacker
		if hijacker, ok := w.(http.Hijacker); ok {
			_, _, _ = hijacker.Hijack()
			hijackedCalled = true
		}

		// Verify Unwrap
		if unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter }); ok {
			if unwrapper.Unwrap() == mock {
				unwrapSucceeded = true
			}
		}

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	handler.ServeHTTP(mock, req)

	if !flushedCalled || !mock.flushed {
		t.Errorf("Expected http.Flusher.Flush to be propagated to underlying writer")
	}
	if !hijackedCalled || !mock.hijacked {
		t.Errorf("Expected http.Hijacker.Hijack to be propagated to underlying writer")
	}
	if !unwrapSucceeded {
		t.Errorf("Expected Unwrap() to return the underlying ResponseWriter")
	}
}
