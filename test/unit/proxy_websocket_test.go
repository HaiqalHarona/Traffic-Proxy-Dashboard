package unit_test

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/metrics"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/proxy"
	"github.com/HaiqalHarona/Traffic-Proxy-Dashboard/internal/server"
)

func setupMockWSServer(t *testing.T, rejectStatusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rejectStatusCode > 0 {
			w.WriteHeader(rejectStatusCode)
			_, _ = w.Write([]byte("rejected upgrade"))
			return
		}

		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("backend writer does not implement http.Hijacker")
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			t.Fatalf("backend hijack failed: %v", err)
		}
		defer conn.Close()

		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n"
		_, _ = bufrw.WriteString(resp)
		_ = bufrw.Flush()

		for {
			line, err := bufrw.ReadString('\n')
			if err != nil {
				return
			}
			_, _ = bufrw.WriteString("ECHO:" + line)
			_ = bufrw.Flush()
		}
	}))
}

func TestRouter_WebSocket_UpgradeSuccess(t *testing.T) {
	t.Parallel()

	backend := setupMockWSServer(t, 0)
	defer backend.Close()

	backendURL, _ := url.Parse(backend.URL)
	collector := metrics.NewCollector()
	router := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 10,
		MaxConcurrentUpgrades: 10,
		QueueTimeout:          time.Second,
	}, collector)

	router.RegisterBackend("ws.local", backendURL)

	frontend := httptest.NewServer(router)
	defer frontend.Close()

	conn, err := net.Dial("tcp", frontend.Listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to dial frontend: %v", err)
	}
	defer conn.Close()

	req := "GET / HTTP/1.1\r\n" +
		"Host: ws.local\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"

	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101, got %d", resp.StatusCode)
	}

	time.Sleep(20 * time.Millisecond)
	if up := collector.ActiveUpgrades.Load(); up != 1 {
		t.Fatalf("expected ActiveUpgrades=1, got %d", up)
	}

	if _, err := conn.Write([]byte("ping\n")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	line, err := reader.ReadString('\n')
	if err != nil || line != "ECHO:ping\n" {
		t.Fatalf("expected 'ECHO:ping\\n', got %q, err: %v", line, err)
	}

	_ = conn.Close()
	time.Sleep(30 * time.Millisecond)
	if up := collector.ActiveUpgrades.Load(); up != 0 {
		t.Fatalf("expected ActiveUpgrades=0 after disconnect, got %d", up)
	}
}

func TestRouter_WebSocket_SemaphoreBypass(t *testing.T) {
	t.Parallel()

	wsBackend := setupMockWSServer(t, 0)
	defer wsBackend.Close()

	var httpHandled atomic.Int64
	httpBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpHandled.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer httpBackend.Close()

	collector := metrics.NewCollector()
	router := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 1, // Only 1 HTTP request slot
		MaxConcurrentUpgrades: 5,
		QueueTimeout:          100 * time.Millisecond,
	}, collector)

	wsURL, _ := url.Parse(wsBackend.URL)
	httpURL, _ := url.Parse(httpBackend.URL)
	router.RegisterBackend("ws.local", wsURL)
	router.RegisterBackend("api.local", httpURL)

	frontend := httptest.NewServer(router)
	defer frontend.Close()

	conn, err := net.Dial("tcp", frontend.Listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	req := "GET / HTTP/1.1\r\nHost: ws.local\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"
	_, _ = conn.Write([]byte(req))

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101, got %v, err: %v", resp, err)
	}

	client := frontend.Client()
	for i := 0; i < 5; i++ {
		httpReq, _ := http.NewRequest(http.MethodGet, frontend.URL+"/", nil)
		httpReq.Host = "api.local"
		res, err := client.Do(httpReq)
		if err != nil {
			t.Fatalf("HTTP request %d failed: %v", i, err)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK while WS is active, got %d (semaphore leaked!)", res.StatusCode)
		}
		_ = res.Body.Close()
	}

	if httpHandled.Load() != 5 {
		t.Fatalf("expected 5 HTTP requests handled, got %d", httpHandled.Load())
	}
}

func TestRouter_WebSocket_Non101_Rejection(t *testing.T) {
	t.Parallel()

	backend := setupMockWSServer(t, http.StatusUnauthorized)
	defer backend.Close()

	backendURL, _ := url.Parse(backend.URL)
	collector := metrics.NewCollector()
	router := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 2,
		QueueTimeout:          time.Second,
	}, collector)
	router.RegisterBackend("ws.local", backendURL)

	frontend := httptest.NewServer(router)
	defer frontend.Close()

	conn, _ := net.Dial("tcp", frontend.Listener.Addr().String())
	defer conn.Close()

	req := "GET / HTTP/1.1\r\nHost: ws.local\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"
	_, _ = conn.Write([]byte(req))

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", resp.StatusCode)
	}

	if up := collector.ActiveUpgrades.Load(); up != 0 {
		t.Fatalf("expected ActiveUpgrades=0 on rejection, got %d", up)
	}
}

func TestRouter_WebSocket_MaxUpgradesExceeded(t *testing.T) {
	t.Parallel()

	backend := setupMockWSServer(t, 0)
	defer backend.Close()

	backendURL, _ := url.Parse(backend.URL)
	collector := metrics.NewCollector()
	router := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 10,
		MaxConcurrentUpgrades: 1, // Only 1 upgrade allowed
		QueueTimeout:          time.Second,
	}, collector)
	router.RegisterBackend("ws.local", backendURL)

	frontend := httptest.NewServer(router)
	defer frontend.Close()

	conn1, _ := net.Dial("tcp", frontend.Listener.Addr().String())
	defer conn1.Close()
	req1 := "GET / HTTP/1.1\r\nHost: ws.local\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"
	_, _ = conn1.Write([]byte(req1))
	resp1, _ := http.ReadResponse(bufio.NewReader(conn1), &http.Request{Method: "GET"})
	if resp1.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101, got %d", resp1.StatusCode)
	}

	conn2, _ := net.Dial("tcp", frontend.Listener.Addr().String())
	defer conn2.Close()
	req2 := "GET / HTTP/1.1\r\nHost: ws.local\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"
	_, _ = conn2.Write([]byte(req2))
	resp2, _ := http.ReadResponse(bufio.NewReader(conn2), &http.Request{Method: "GET"})
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable when upgrade limit exceeded, got %d", resp2.StatusCode)
	}
}

func TestSetupRouter_WebSocket_HostParamUpgradeSuccess(t *testing.T) {
	t.Parallel()

	backend := setupMockWSServer(t, 0)
	defer backend.Close()

	backendURL, _ := url.Parse(backend.URL)
	collector := metrics.NewCollector()
	router := proxy.NewRouter(proxy.Config{
		MaxConcurrentRequests: 10,
		MaxConcurrentUpgrades: 10,
		QueueTimeout:          time.Second,
	}, collector)

	router.RegisterBackend("ws.local", backendURL)

	// Wrap inside full Chi router SetupRouter to verify end-to-end server routing
	mux := server.SetupRouter(collector, router, nil)
	frontend := httptest.NewServer(mux)
	defer frontend.Close()

	conn, err := net.Dial("tcp", frontend.Listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to dial frontend: %v", err)
	}
	defer conn.Close()

	// Simulate browser connecting to ws://localhost:8080/?host=ws.local
	req := "GET /?host=ws.local HTTP/1.1\r\n" +
		"Host: " + frontend.Listener.Addr().String() + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"

	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101 Switching Protocols through SetupRouter, got %d", resp.StatusCode)
	}

	time.Sleep(20 * time.Millisecond)
	if up := collector.ActiveUpgrades.Load(); up != 1 {
		t.Fatalf("expected ActiveUpgrades=1, got %d", up)
	}

	if _, err := conn.Write([]byte("hello from dashboard\n")); err != nil {
		t.Fatalf("write frame failed: %v", err)
	}
	line, err := reader.ReadString('\n')
	if err != nil || line != "ECHO:hello from dashboard\n" {
		t.Fatalf("expected 'ECHO:hello from dashboard\\n', got %q, err: %v", line, err)
	}
}
