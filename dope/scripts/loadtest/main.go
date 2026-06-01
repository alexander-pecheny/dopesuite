// Command loadtest drives a concurrent load test against a running dope server:
// it holds N long-lived SSE viewer connections open while M editors push game
// state edits, and measures the metrics that actually matter for this app on
// its single-core VPS:
//
//   - edit latency (the editor PUT round trip) and error rate, which exposes
//     contention on the global write mutex + the single SQLite writer;
//   - end-to-end propagation latency: the time from an editor's send to a
//     viewer actually receiving that edit over its SSE stream. Editors stamp
//     each payload with a sequence number and a send timestamp; viewers read
//     them back out of the broadcast. Editors and viewers share this process's
//     clock, so the delta is accurate with no clock-sync needed;
//   - delivery ratio: fraction of (edit, viewer) pairs that arrived. The
//     server intentionally drops the oldest queued event when a slow viewer's
//     8-slot channel is full, so gaps here flag fan-out backpressure.
//
// Run this from a machine OTHER than the VPS so it exercises nginx + the
// network the way real clients do. Provision a disposable public fest and the
// editor session tokens first with provision.py (run on the VPS).
//
//	go run ./scripts/loadtest \
//	  -base https://dope.pecheny.me \
//	  -fest-id 42 -fest dope-loadtest-260602 -game 99 \
//	  -viewers 120 -editors 3 -edit-interval 2s -duration 90s \
//	  -tokens tok1,tok2,tok3
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type config struct {
	base         string
	festRef      string
	festID       string
	gameID       string
	viewers      int
	editors      int
	editInterval time.Duration
	duration     time.Duration
	ramp         time.Duration
	tokens       []string
	payloadBytes int
	outPath      string
	insecure     bool
}

// eventEnvelope mirrors the server's SSE payload wrapper (db.go eventEnvelope).
type eventEnvelope struct {
	Scope    string          `json:"scope"`
	Revision int64           `json:"revision"`
	Data     json.RawMessage `json:"data"`
}

// marker is embedded at the top level of every editor payload and echoed back
// verbatim to viewers in the broadcast, letting us correlate send -> receive.
type marker struct {
	Seq    int64 `json:"_lt_seq"`
	TSNano int64 `json:"_lt_ts"`
	Editor int   `json:"_lt_editor"`
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	var cfg config
	var tokens string
	flag.StringVar(&cfg.base, "base", "https://dope.pecheny.me", "server base URL")
	flag.StringVar(&cfg.festRef, "fest", "", "fest slug or id used in /api/fest/{ref} edit paths")
	flag.StringVar(&cfg.festID, "fest-id", "", "numeric fest id used in /events?fest_id= (defaults to -fest)")
	flag.StringVar(&cfg.gameID, "game", "", "game id to edit")
	flag.IntVar(&cfg.viewers, "viewers", 100, "number of concurrent SSE viewers")
	flag.IntVar(&cfg.editors, "editors", 3, "number of concurrent editors")
	flag.DurationVar(&cfg.editInterval, "edit-interval", 2*time.Second, "delay between edits per editor (jittered ±25%)")
	flag.DurationVar(&cfg.duration, "duration", 60*time.Second, "total test duration")
	flag.DurationVar(&cfg.ramp, "ramp", 5*time.Second, "spread viewer connects over this window to avoid a thundering herd")
	flag.StringVar(&tokens, "tokens", "", "comma-separated editor session tokens (one per editor; reused round-robin)")
	flag.IntVar(&cfg.payloadBytes, "payload-bytes", 1200, "approximate edit payload size, padded to model a real game-state blob")
	flag.StringVar(&cfg.outPath, "out", "", "optional path to write the JSON report")
	flag.BoolVar(&cfg.insecure, "insecure", false, "skip TLS verification")
	flag.Parse()

	if cfg.festID == "" {
		cfg.festID = cfg.festRef
	}
	for _, t := range strings.Split(tokens, ",") {
		if t = strings.TrimSpace(t); t != "" {
			cfg.tokens = append(cfg.tokens, t)
		}
	}
	if cfg.festRef == "" || cfg.gameID == "" {
		fmt.Fprintln(os.Stderr, "loadtest: -fest and -game are required")
		os.Exit(2)
	}
	if cfg.editors > 0 && len(cfg.tokens) == 0 {
		fmt.Fprintln(os.Stderr, "loadtest: -tokens is required when -editors > 0")
		os.Exit(2)
	}
	return cfg
}

func run(cfg config) error {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.duration)
	defer cancel()

	stats := newStats()
	var wg sync.WaitGroup

	// Viewers: a dedicated client with no response-header/idle timeout so SSE
	// streams stay open for the full run. Connections per host are uncapped so
	// 100+ viewers each get their own socket.
	viewerClient := newHTTPClient(cfg, 0, cfg.viewers+cfg.editors+10)
	rng := rand.New(rand.NewSource(1)) // fixed seed: reproducible ramp jitter
	for i := 0; i < cfg.viewers; i++ {
		delay := time.Duration(0)
		if cfg.ramp > 0 {
			delay = time.Duration(rng.Int63n(int64(cfg.ramp)))
		}
		wg.Add(1)
		go func(id int, start time.Duration) {
			defer wg.Done()
			select {
			case <-time.After(start):
			case <-ctx.Done():
				return
			}
			runViewer(ctx, cfg, viewerClient, stats)
		}(i, delay)
	}

	// Editors: a separate client with a sane per-request timeout so a stuck
	// write surfaces as a failure rather than hanging the whole run.
	editorClient := newHTTPClient(cfg, 30*time.Second, cfg.editors+4)
	for i := 0; i < cfg.editors; i++ {
		token := cfg.tokens[i%len(cfg.tokens)]
		wg.Add(1)
		go func(id int, tok string) {
			defer wg.Done()
			runEditor(ctx, cfg, editorClient, stats, id, tok, rand.New(rand.NewSource(int64(id)+1)))
		}(i, token)
	}

	fmt.Printf("running: %d viewers, %d editors, %s, target %s\n", cfg.viewers, cfg.editors, cfg.duration, cfg.base)
	done := make(chan struct{})
	go progress(ctx, stats, done)
	wg.Wait()
	close(done)

	report := stats.finalize(cfg)
	report.print()
	if cfg.outPath != "" {
		if err := report.writeJSON(cfg.outPath); err != nil {
			return err
		}
		fmt.Println("report written to", cfg.outPath)
	}
	return nil
}

func newHTTPClient(cfg config, timeout time.Duration, maxConns int) *http.Client {
	tr := &http.Transport{
		MaxIdleConns:        maxConns,
		MaxIdleConnsPerHost: maxConns,
		MaxConnsPerHost:     maxConns,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   false, // keep HTTP/1.1 so connection accounting matches nginx
	}
	if cfg.insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

// runViewer opens one SSE stream and records every marker it sees until the
// context expires or the connection drops.
func runViewer(ctx context.Context, cfg config, client *http.Client, stats *stats) {
	url := fmt.Sprintf("%s/events?fest_id=%s", strings.TrimRight(cfg.base, "/"), cfg.festID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		stats.viewerFailed.Add(1)
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			stats.viewerFailed.Add(1)
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		stats.viewerFailed.Add(1)
		return
	}
	stats.viewerConnected.Add(1)

	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	var dataBuf bytes.Buffer
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if ctx.Err() == nil {
				stats.viewerDropped.Add(1)
			}
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			// End of one SSE event: process accumulated data field.
			if dataBuf.Len() > 0 {
				stats.recordEvent(dataBuf.Bytes())
				dataBuf.Reset()
			}
		case strings.HasPrefix(line, "data:"):
			dataBuf.WriteString(strings.TrimSpace(line[len("data:"):]))
		default:
			// "event:", "id:", and ": comment" keepalives — ignored.
		}
	}
}

// runEditor pushes edits at roughly editInterval, each stamped with a unique
// sequence and the current time so viewers can compute propagation latency.
func runEditor(ctx context.Context, cfg config, client *http.Client, stats *stats, id int, token string, rng *rand.Rand) {
	url := fmt.Sprintf("%s/api/fest/%s/games/%s/state",
		strings.TrimRight(cfg.base, "/"), cfg.festRef, cfg.gameID)
	pad := strings.Repeat("x", cfg.payloadBytes)
	for {
		// Jitter ±25% so editors don't march in lockstep.
		jitter := time.Duration(rng.Int63n(int64(cfg.editInterval)/2+1)) - cfg.editInterval/4
		select {
		case <-time.After(cfg.editInterval + jitter):
		case <-ctx.Done():
			return
		}

		seq := stats.editSeq.Add(1)
		body := buildPayload(marker{Seq: seq, TSNano: time.Now().UnixNano(), Editor: id}, pad)
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", "session="+token)
		// Deliberately omit Origin: the same-origin guard only fires when it is
		// present, and a headless client legitimately has none.

		start := time.Now()
		resp, err := client.Do(req)
		elapsed := time.Since(start)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			stats.recordEdit(elapsed, 0)
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		stats.recordEdit(elapsed, resp.StatusCode)
	}
}

func buildPayload(m marker, pad string) []byte {
	// A flat object the server stores verbatim and rebroadcasts: marker fields
	// at the top level (so viewers can read them straight off the envelope's
	// data) plus padding to model a realistic game-state blob size.
	obj := map[string]any{
		"_lt_seq":    m.Seq,
		"_lt_ts":     m.TSNano,
		"_lt_editor": m.Editor,
		"loadtest":   true,
		"pad":        pad,
	}
	b, _ := json.Marshal(obj)
	return b
}

// ---- metrics ----

type stats struct {
	editSeq atomic.Int64

	viewerConnected atomic.Int64
	viewerFailed    atomic.Int64
	viewerDropped   atomic.Int64

	eventsReceived atomic.Int64 // all SSE state events seen across viewers
	markersSeen    atomic.Int64 // events carrying our loadtest marker

	editsOK    atomic.Int64
	editsBusy  atomic.Int64 // 5xx — the signature of write-lock/contention failures
	editsOther atomic.Int64 // non-2xx, non-5xx (auth, 404, etc.)
	editsErr   atomic.Int64 // transport error / timeout (status 0)

	mu        sync.Mutex
	editMS    []float64 // edit round-trip latencies, ms
	propMS    []float64 // propagation latencies (edit send -> viewer receive), ms
	startTime time.Time
}

func newStats() *stats {
	return &stats{startTime: time.Now()}
}

func (s *stats) recordEdit(elapsed time.Duration, status int) {
	switch {
	case status == 0:
		s.editsErr.Add(1)
	case status >= 200 && status < 300:
		s.editsOK.Add(1)
	case status >= 500:
		s.editsBusy.Add(1)
	default:
		s.editsOther.Add(1)
	}
	s.mu.Lock()
	s.editMS = append(s.editMS, float64(elapsed.Microseconds())/1000)
	s.mu.Unlock()
}

func (s *stats) recordEvent(data []byte) {
	s.eventsReceived.Add(1)
	var env eventEnvelope
	if err := json.Unmarshal(data, &env); err != nil || len(env.Data) == 0 {
		return
	}
	var m marker
	if err := json.Unmarshal(env.Data, &m); err != nil || m.TSNano == 0 {
		return
	}
	s.markersSeen.Add(1)
	latency := float64(time.Now().UnixNano()-m.TSNano) / 1e6
	s.mu.Lock()
	s.propMS = append(s.propMS, latency)
	s.mu.Unlock()
}

type report struct {
	DurationSec     float64 `json:"duration_sec"`
	Viewers         int     `json:"viewers"`
	ViewersConn     int64   `json:"viewers_connected"`
	ViewersFailed   int64   `json:"viewers_failed"`
	ViewersDropped  int64   `json:"viewers_dropped_midrun"`
	Editors         int     `json:"editors"`
	EditsTotal      int64   `json:"edits_total"`
	EditsOK         int64   `json:"edits_ok"`
	Edits5xx        int64   `json:"edits_5xx_busy"`
	EditsOther      int64   `json:"edits_other_4xx"`
	EditsErr        int64   `json:"edits_transport_err"`
	EditThroughput  float64 `json:"edit_throughput_per_sec"`
	EditP50         float64 `json:"edit_latency_ms_p50"`
	EditP95         float64 `json:"edit_latency_ms_p95"`
	EditP99         float64 `json:"edit_latency_ms_p99"`
	EditMax         float64 `json:"edit_latency_ms_max"`
	EventsReceived  int64   `json:"sse_events_received"`
	MarkersSeen     int64   `json:"sse_markers_seen"`
	DeliveryRatio   float64 `json:"delivery_ratio"`
	PropP50         float64 `json:"propagation_ms_p50"`
	PropP95         float64 `json:"propagation_ms_p95"`
	PropP99         float64 `json:"propagation_ms_p99"`
	PropMax         float64 `json:"propagation_ms_max"`
	ExpectedDeliver int64   `json:"expected_deliveries"`
}

func (s *stats) finalize(cfg config) report {
	s.mu.Lock()
	defer s.mu.Unlock()
	dur := time.Since(s.startTime).Seconds()
	total := s.editsOK.Load() + s.editsBusy.Load() + s.editsOther.Load() + s.editsErr.Load()
	conn := s.viewerConnected.Load()
	okEdits := s.editsOK.Load()
	// Each successful edit should reach every viewer that was connected.
	expected := okEdits * conn
	ratio := 0.0
	if expected > 0 {
		ratio = float64(s.markersSeen.Load()) / float64(expected)
	}
	r := report{
		DurationSec:     dur,
		Viewers:         cfg.viewers,
		ViewersConn:     conn,
		ViewersFailed:   s.viewerFailed.Load(),
		ViewersDropped:  s.viewerDropped.Load(),
		Editors:         cfg.editors,
		EditsTotal:      total,
		EditsOK:         okEdits,
		Edits5xx:        s.editsBusy.Load(),
		EditsOther:      s.editsOther.Load(),
		EditsErr:        s.editsErr.Load(),
		EventsReceived:  s.eventsReceived.Load(),
		MarkersSeen:     s.markersSeen.Load(),
		ExpectedDeliver: expected,
		DeliveryRatio:   ratio,
	}
	if dur > 0 {
		r.EditThroughput = float64(okEdits) / dur
	}
	r.EditP50, r.EditP95, r.EditP99, r.EditMax = percentiles(s.editMS)
	r.PropP50, r.PropP95, r.PropP99, r.PropMax = percentiles(s.propMS)
	return r
}

func percentiles(v []float64) (p50, p95, p99, max float64) {
	if len(v) == 0 {
		return 0, 0, 0, 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	pick := func(p float64) float64 {
		idx := int(p * float64(len(s)-1))
		return s[idx]
	}
	return pick(0.50), pick(0.95), pick(0.99), s[len(s)-1]
}

func (r report) print() {
	fmt.Println("\n================ load test report ================")
	fmt.Printf("duration            %.1fs\n", r.DurationSec)
	fmt.Printf("viewers             %d requested, %d connected, %d failed, %d dropped mid-run\n",
		r.Viewers, r.ViewersConn, r.ViewersFailed, r.ViewersDropped)
	fmt.Println("---- edits (write path / SQLite + global write mutex) ----")
	fmt.Printf("editors             %d\n", r.Editors)
	fmt.Printf("edits               %d total | %d ok | %d 5xx/busy | %d 4xx | %d transport-err\n",
		r.EditsTotal, r.EditsOK, r.Edits5xx, r.EditsOther, r.EditsErr)
	fmt.Printf("edit throughput     %.1f ok/s\n", r.EditThroughput)
	fmt.Printf("edit latency ms     p50=%.0f  p95=%.0f  p99=%.0f  max=%.0f\n",
		r.EditP50, r.EditP95, r.EditP99, r.EditMax)
	fmt.Println("---- propagation (edit -> viewer over SSE) ----")
	fmt.Printf("sse events recv     %d (%d carried markers)\n", r.EventsReceived, r.MarkersSeen)
	fmt.Printf("delivery ratio      %.3f  (%d seen / %d expected = ok_edits*viewers)\n",
		r.DeliveryRatio, r.MarkersSeen, r.ExpectedDeliver)
	fmt.Printf("propagation ms      p50=%.0f  p95=%.0f  p99=%.0f  max=%.0f\n",
		r.PropP50, r.PropP95, r.PropP99, r.PropMax)
	fmt.Println("==================================================")
}

func (r report) writeJSON(path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// progress prints a one-line heartbeat each second so a long run shows life.
func progress(ctx context.Context, s *stats, done chan struct{}) {
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			fmt.Printf("\r  conn=%d edits_ok=%d 5xx=%d events=%d   ",
				s.viewerConnected.Load(), s.editsOK.Load(), s.editsBusy.Load(), s.eventsReceived.Load())
		case <-ctx.Done():
			return
		case <-done:
			fmt.Print("\r")
			return
		}
	}
}
