// loadgen — a tiny HTTP/1.1 keepalive load generator.
//
// Each worker opens one TCP connection and pipelines requests back-to-back
// for the entire duration. We measure throughput (requests/sec) and a few
// latency percentiles computed from per-request RTT samples.
//
// Usage:
//
//	loadgen -addr 127.0.0.1:8080 -c 64 -d 10s [-warm 1s]
//
// Output: a single JSON object on stdout, e.g.
//
//	{"rps":123456.78,"requests":1234567,"p50_us":120,"p99_us":900,"errors":0}
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "host:port to hit")
	conc := flag.Int("c", 64, "concurrent connections")
	pipe := flag.Int("pipeline", 16, "requests to pipeline per connection")
	dur := flag.Duration("d", 10*time.Second, "test duration")
	warm := flag.Duration("warm", 1*time.Second, "warmup duration (not counted)")
	host := flag.String("host", "", "Host header (defaults to -addr)")
	flag.Parse()

	hostHeader := *host
	if hostHeader == "" {
		hostHeader = *addr
	}
	single := []byte("GET / HTTP/1.1\r\nHost: " + hostHeader + "\r\n\r\n")
	batch := bytes.Repeat(single, *pipe)

	stop := make(chan struct{})
	var requests, errors uint64
	var sampleMu sync.Mutex
	var samples []int64 // microseconds, only collected after warmup

	measuring := &atomic.Bool{}

	var wg sync.WaitGroup
	for i := 0; i < *conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.Dial("tcp", *addr)
			if err != nil {
				atomic.AddUint64(&errors, 1)
				return
			}
			defer conn.Close()
			br := bufio.NewReaderSize(conn, 8192)
			local := make([]int64, 0, 1024)

			for {
				select {
				case <-stop:
					if len(local) > 0 {
						sampleMu.Lock()
						samples = append(samples, local...)
						sampleMu.Unlock()
					}
					return
				default:
				}
				start := time.Now()
				if _, err := conn.Write(batch); err != nil {
					atomic.AddUint64(&errors, 1)
					return
				}
				ok := true
				for i := 0; i < *pipe; i++ {
					if err := readOneResponse(br); err != nil {
						atomic.AddUint64(&errors, 1)
						ok = false
						break
					}
				}
				if !ok {
					return
				}
				atomic.AddUint64(&requests, uint64(*pipe))
				if measuring.Load() {
					// Latency per request within the batch (average over batch).
					local = append(local, time.Since(start).Microseconds()/int64(*pipe))
					if len(local) >= 4096 {
						sampleMu.Lock()
						samples = append(samples, local...)
						sampleMu.Unlock()
						local = local[:0]
					}
				}
			}
		}()
	}

	// Warmup phase: discard counts.
	time.Sleep(*warm)
	atomic.StoreUint64(&requests, 0)
	atomic.StoreUint64(&errors, 0)
	measuring.Store(true)
	t0 := time.Now()

	time.Sleep(*dur)
	close(stop)
	elapsed := time.Since(t0).Seconds()
	wg.Wait()

	r := atomic.LoadUint64(&requests)
	e := atomic.LoadUint64(&errors)
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	pct := func(p float64) int64 {
		if len(samples) == 0 {
			return 0
		}
		i := int(float64(len(samples)) * p)
		if i >= len(samples) {
			i = len(samples) - 1
		}
		return samples[i]
	}

	out := map[string]any{
		"rps":      float64(r) / elapsed,
		"requests": r,
		"errors":   e,
		"seconds":  elapsed,
		"p50_us":   pct(0.50),
		"p90_us":   pct(0.90),
		"p99_us":   pct(0.99),
		"conc":     *conc,
	}
	// Emit BOTH a JSON line (for archival) and shell-friendly key=value lines
	// (for easy extraction in the runner script).
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(out)
	fmt.Printf("rps=%.2f\n", out["rps"])
	fmt.Printf("requests=%d\n", r)
	fmt.Printf("errors=%d\n", e)
	fmt.Printf("seconds=%.3f\n", out["seconds"])
	fmt.Printf("p50_us=%d\n", out["p50_us"])
	fmt.Printf("p90_us=%d\n", out["p90_us"])
	fmt.Printf("p99_us=%d\n", out["p99_us"])
	fmt.Printf("conc=%d\n", *conc)
}

func readOneResponse(br *bufio.Reader) error {
	var contentLen int = -1
	// Read status line + headers
	for {
		line, err := br.ReadSlice('\n')
		if err != nil {
			return err
		}
		if len(line) <= 2 {
			break
		}
		if bytes.HasPrefix(bytes.ToLower(line), []byte("content-length:")) {
			v := bytes.TrimSpace(line[len("content-length:"):])
			n := 0
			for _, c := range v {
				if c < '0' || c > '9' {
					break
				}
				n = n*10 + int(c-'0')
			}
			contentLen = n
		}
	}
	if contentLen > 0 {
		if _, err := br.Discard(contentLen); err != nil {
			return err
		}
	}
	return nil
}

var _ = fmt.Sprintf // keep fmt imported for debugging if needed
