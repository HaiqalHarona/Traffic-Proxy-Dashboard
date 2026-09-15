package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type stats struct {
	totalRequests atomic.Uint64
	status200     atomic.Uint64
	status502     atomic.Uint64
	status503     atomic.Uint64
	statusOther   atomic.Uint64
	errors        atomic.Uint64
}

func main() {
	target := flag.String("target", "http://localhost:80", "Base URL of TrafficProxy")
	concurrency := flag.Int("concurrency", 15, "Number of concurrent traffic worker goroutines")
	duration := flag.Duration("duration", 15*time.Second, "Test duration (0 for infinite until SIGINT)")
	testSSE := flag.Bool("sse", true, "Whether to launch a concurrent SSE telemetry subscriber")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if *duration > 0 {
		var timeoutCancel context.CancelFunc
		ctx, timeoutCancel = context.WithTimeout(ctx, *duration)
		defer timeoutCancel()
	}

	fmt.Printf("== TrafficProxy Traffic Generator ==\n")
	fmt.Printf("Target:       %s\n", *target)
	fmt.Printf("Concurrency:  %d workers\n", *concurrency)
	if *duration > 0 {
		fmt.Printf("Duration:     %s\n", *duration)
	} else {
		fmt.Printf("Duration:     Infinite (press Ctrl+C to stop)\n")
	}
	fmt.Printf("=====================================\n\n")

	var s stats
	var wg sync.WaitGroup

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        200,
			MaxIdleConnsPerHost: 50,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	// Launch SSE subscriber goroutine to test SSE stream
	if *testSSE {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runSSESubscriber(ctx, *target)
		}()
	}

	// Launch concurrent HTTP traffic workers
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			runWorker(ctx, client, *target, &s)
		}(i)
	}

	// Status reporter ticker
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	startTime := time.Now()

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-ticker.C:
			elapsed := time.Since(startTime).Seconds()
			total := s.totalRequests.Load()
			rps := float64(total) / elapsed
			fmt.Printf("\r[%.0fs] Total: %d (%.1f req/s) | 200 OK: %d | 502 Bad Gateway: %d | 503 Overloaded: %d | Errors: %d",
				elapsed, total, rps, s.status200.Load(), s.status502.Load(), s.status503.Load(), s.errors.Load())
		}
	}

	wg.Wait()

	elapsed := time.Since(startTime).Seconds()
	total := s.totalRequests.Load()
	fmt.Printf("\n\n== Final Results (Duration: %.2fs) ==\n", elapsed)
	fmt.Printf("Total Requests Sent: %d\n", total)
	fmt.Printf("Average Throughput:  %.1f req/s\n", float64(total)/elapsed)
	fmt.Printf("200 OK (Healthy):    %d\n", s.status200.Load())
	fmt.Printf("502 Bad Gateway:     %d\n", s.status502.Load())
	fmt.Printf("503 Overloaded:      %d\n", s.status503.Load())
	fmt.Printf("Other Responses:     %d\n", s.statusOther.Load())
	fmt.Printf("Network Errors:      %d\n", s.errors.Load())
	fmt.Printf("=====================================\n")
}

func runWorker(ctx context.Context, client *http.Client, targetURL string, s *stats) {
	// Weighted traffic simulation routes:
	// 50% -> app.local (healthy fast backend)
	// 30% -> slow.local (causes concurrency queue saturation)
	// 10% -> unknown.local (tests 502 unmapped route handling)
	// 10% -> UI routes (/ and /static/index.html)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		roll := rand.Intn(100)
		var req *http.Request
		var err error

		switch {
		case roll < 50:
			req, err = http.NewRequestWithContext(ctx, http.MethodGet, targetURL+"/", nil)
			if err == nil {
				req.Host = "app.local"
			}
		case roll < 80:
			req, err = http.NewRequestWithContext(ctx, http.MethodGet, targetURL+"/", nil)
			if err == nil {
				req.Host = "slow.local"
			}
		case roll < 90:
			req, err = http.NewRequestWithContext(ctx, http.MethodGet, targetURL+"/unregistered", nil)
			if err == nil {
				req.Host = "unknown.local"
			}
		default:
			// Directly test static Chi handler
			req, err = http.NewRequestWithContext(ctx, http.MethodGet, targetURL+"/", nil)
		}

		if err != nil {
			s.errors.Add(1)
			continue
		}

		s.totalRequests.Add(1)
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() == nil {
				s.errors.Add(1)
			}
			continue
		}

		// Discard body
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		switch resp.StatusCode {
		case http.StatusOK:
			s.status200.Add(1)
		case http.StatusBadGateway:
			s.status502.Add(1)
		case http.StatusServiceUnavailable:
			s.status503.Add(1)
		default:
			s.statusOther.Add(1)
		}

		// Small jitter between 5ms - 25ms to simulate realistic web traffic
		time.Sleep(time.Duration(5+rand.Intn(20)) * time.Millisecond)
	}
}

func runSSESubscriber(ctx context.Context, targetURL string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL+"/api/events", nil)
	if err != nil {
		return
	}
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{
		Timeout: 0, // No timeout for streaming
	}

	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, err := reader.ReadString('\n')
		if err != nil {
			return
		}
	}
}
