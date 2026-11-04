package main

import (
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// App is one launchable app. Path is an opaque id (on Windows, the Start menu AppID).
type App struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

var (
	statsMu   sync.Mutex
	history   [][2]float64 // [rx Mbps, tx Mbps] per second, newest last
	lastRx    uint64
	lastTx    uint64
	lastAt    time.Time
	speed     = map[string]interface{}{}
	testing   bool
	batteryV  = []map[string]interface{}{}
	musicV    = map[string]interface{}{}
	musicLock sync.Mutex
)

func startStats() {
	go func() {
		for range time.Tick(time.Second) {
			sampleNetwork()
		}
	}()
	go func() {
		for {
			b := batteries()
			statsMu.Lock()
			batteryV = b
			statsMu.Unlock()
			time.Sleep(60 * time.Second)
		}
	}()
	go func() {
		for {
			refreshMusic()
			time.Sleep(2 * time.Second)
		}
	}()
	go func() { // first speed test shortly after launch so the widget isn't empty
		time.Sleep(5 * time.Second)
		runSpeedTest()
	}()
}

func refreshMusic() {
	m := nowPlaying()
	musicLock.Lock()
	musicV = m
	musicLock.Unlock()
}

func statsSnapshot() map[string]interface{} {
	statsMu.Lock()
	s := map[string]interface{}{}
	for k, v := range speed {
		s[k] = v
	}
	s["running"] = testing
	hist := make([]float64, len(history))
	var rx, tx float64
	for i, h := range history {
		hist[i] = h[0]
	}
	if n := len(history); n > 0 {
		rx, tx = history[n-1][0], history[n-1][1]
	}
	bats := batteryV
	statsMu.Unlock()
	musicLock.Lock()
	m := musicV
	musicLock.Unlock()
	return map[string]interface{}{
		"net":       map[string]interface{}{"rx": rx, "tx": tx, "history": hist},
		"speed":     s,
		"batteries": bats,
		"mac":       hostName(), // the page reads this field for the computer's name
		"music":     m,
		"status":    deckStatus(),
	}
}

func sampleNetwork() {
	rx, tx, ok := netCounters()
	if !ok {
		return
	}
	now := time.Now()
	statsMu.Lock()
	defer statsMu.Unlock()
	if !lastAt.IsZero() {
		dt := now.Sub(lastAt).Seconds()
		d := func(cur, prev uint64) float64 { // 32-bit counters can wrap
			if cur >= prev {
				return float64(cur - prev)
			}
			return float64(cur + (1 << 32) - prev)
		}
		drx, dtx := d(rx, lastRx), d(tx, lastTx)
		if drx > 5e9 || dtx > 5e9 { // interface changed; skip this sample
			drx, dtx = 0, 0
		}
		history = append(history, [2]float64{drx * 8 / dt / 1e6, dtx * 8 / dt / 1e6})
		if len(history) > 60 {
			history = history[len(history)-60:]
		}
	}
	lastRx, lastTx, lastAt = rx, tx, now
}

// ---------- speed test (Cloudflare's public speed endpoints; Windows has no networkQuality) ----------

const speedHost = "https://speed.cloudflare.com"

type countingReader struct {
	r io.Reader
	n *int64
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	atomic.AddInt64(c.n, int64(n))
	return n, err
}

func runSpeedTest() {
	statsMu.Lock()
	if testing {
		statsMu.Unlock()
		return
	}
	testing = true
	statsMu.Unlock()

	result := map[string]interface{}{}
	client := &http.Client{Timeout: 20 * time.Second}

	// Ping: best of 5 small requests on a warm connection.
	var best time.Duration
	fails := 0
	for i := 0; i < 6 && fails < 2; i++ {
		t0 := time.Now()
		resp, err := client.Get(speedHost + "/__down?bytes=0")
		if err != nil {
			fails++
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if d := time.Since(t0); i > 0 && (best == 0 || d < best) {
			best = d
		}
	}
	if best == 0 { // no internet (or the test site is blocked): keep the last result
		statsMu.Lock()
		testing = false
		statsMu.Unlock()
		return
	}
	result["ping"] = float64(best.Microseconds()) / 1000

	// Download and upload: 4 parallel streams for a fixed time.
	if mbps := streamTest(func(ctx context.Context, n *int64) error {
		req, _ := http.NewRequestWithContext(ctx, "GET", speedHost+"/__down?bytes=25000000", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, countingReader{resp.Body, n})
		return resp.Body.Close()
	}, 8*time.Second); mbps > 0 {
		result["down"] = mbps
	}
	chunk := make([]byte, 4<<20)
	_, _ = rand.Read(chunk)
	if mbps := streamTest(func(ctx context.Context, n *int64) error {
		body := countingReader{&sliceReader{b: chunk}, n}
		req, _ := http.NewRequestWithContext(ctx, "POST", speedHost+"/__up", body)
		req.ContentLength = int64(len(chunk))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.Body.Close()
	}, 6*time.Second); mbps > 0 {
		result["up"] = mbps
	}

	statsMu.Lock()
	testing = false
	if len(result) > 0 {
		result["at"] = float64(time.Now().UnixMilli())
		speed = result
	}
	statsMu.Unlock()
}

type sliceReader struct {
	b   []byte
	off int
}

func (s *sliceReader) Read(p []byte) (int, error) {
	if s.off >= len(s.b) {
		return 0, io.EOF
	}
	n := copy(p, s.b[s.off:])
	s.off += n
	return n, nil
}

// streamTest runs `one` repeatedly on 4 goroutines until `dur` passes and returns Mbps.
func streamTest(one func(ctx context.Context, n *int64) error, dur time.Duration) float64 {
	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()
	var n int64
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				if one(ctx, &n) != nil && ctx.Err() == nil {
					return // offline or blocked: stop this stream instead of retrying in a tight loop
				}
			}
		}()
	}
	wg.Wait()
	secs := time.Since(start).Seconds()
	if secs <= 0 || n == 0 {
		return 0
	}
	return float64(atomic.LoadInt64(&n)) * 8 / secs / 1e6
}
