// Command loadtest drives a concurrent load test against a running dope server:
// it holds long-lived SSE viewer connections open while editors push game state
// edits, and measures the metrics that actually matter for this app on its
// single-core VPS:
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
// Single load level:
//
//	go run ./scripts/loadtest -base https://dope.pecheny.me \
//	  -fest <slug> -fest-id <id> -game <id> -tokens t1,t2,t3 \
//	  -viewers 120 -editors 3 -duration 90s
//
// Cumulative staged ramp (viewers stay connected and grow each stage):
//
//	go run ./scripts/loadtest ... -editors 3 \
//	  -stages 50:24s,100:24s,200:24s,500:24s,1000:24s
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

type stageCfg struct {
	viewers int
	dur     time.Duration
}

type config struct {
	base         string
	festRef      string
	festID       string
	gameID       string
	editors      int
	editInterval time.Duration
	ramp         time.Duration
	tokens       []string
	payloadBytes int
	outPath      string
	insecure     bool
	stages       []stageCfg
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
	var tokens, stages string
	var viewers int
	var duration time.Duration
	flag.StringVar(&cfg.base, "base", "https://dope.pecheny.me", "server base URL")
	flag.StringVar(&cfg.festRef, "fest", "", "fest slug or id used in /api/fest/{ref} edit paths")
	flag.StringVar(&cfg.festID, "fest-id", "", "numeric fest id used in /events?fest_id= (defaults to -fest)")
	flag.StringVar(&cfg.gameID, "game", "", "game id to edit")
	flag.IntVar(&viewers, "viewers", 100, "viewers for a single-level run (ignored when -stages is set)")
	flag.DurationVar(&duration, "duration", 60*time.Second, "duration for a single-level run (ignored when -stages is set)")
	flag.StringVar(&stages, "stages", "", "cumulative ramp as count:dur pairs, e.g. 50:24s,100:24s,200:24s")
	flag.IntVar(&cfg.editors, "editors", 3, "number of concurrent editors (run for the whole test)")
	flag.DurationVar(&cfg.editInterval, "edit-interval", 2*time.Second, "delay between edits per editor (jittered ±25%)")
	flag.DurationVar(&cfg.ramp, "ramp", 5*time.Second, "window to spread each stage's new viewer connects over")
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
	if stages != "" {
		cfg.stages = parseStages(stages)
	} else {
		cfg.stages = []stageCfg{{viewers: viewers, dur: duration}}
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

func parseStages(s string) []stageCfg {
	var out []stageCfg
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		count, durStr, ok := strings.Cut(part, ":")
		if !ok {
			fmt.Fprintf(os.Stderr, "loadtest: bad stage %q, want count:dur\n", part)
			os.Exit(2)
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(count), "%d", &n); err != nil || n < 0 {
			fmt.Fprintf(os.Stderr, "loadtest: bad viewer count in stage %q\n", part)
			os.Exit(2)
		}
		dur, err := time.ParseDuration(strings.TrimSpace(durStr))
		if err != nil {
			fmt.Fprintf(os.Stderr, "loadtest: bad duration in stage %q: %v\n", part, err)
			os.Exit(2)
		}
		out = append(out, stageCfg{viewers: n, dur: dur})
	}
	if len(out) == 0 {
		fmt.Fprintln(os.Stderr, "loadtest: -stages parsed to nothing")
		os.Exit(2)
	}
	return out
}

func run(cfg config) error {
	var total time.Duration
	maxViewers := 0
	for _, st := range cfg.stages {
		total += st.dur
		if st.viewers > maxViewers {
			maxViewers = st.viewers
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), total+5*time.Second)
	defer cancel()

	stats := newStats(cfg.stages)
	var wg sync.WaitGroup

	viewerClient := newHTTPClient(cfg, 0, maxViewers+cfg.editors+10)
	editorClient := newHTTPClient(cfg, 30*time.Second, cfg.editors+4)

	// Editors run for the whole test, attributing each edit to whatever stage
	// is current when it completes.
	for i := 0; i < cfg.editors; i++ {
		token := cfg.tokens[i%len(cfg.tokens)]
		wg.Add(1)
		go func(id int, tok string) {
			defer wg.Done()
			runEditor(ctx, cfg, editorClient, stats, id, tok, rand.New(rand.NewSource(int64(id)+1)))
		}(i, token)
	}

	fmt.Printf("running: %d editors, %d stages, %s total, target %s\n",
		cfg.editors, len(cfg.stages), total, cfg.base)
	done := make(chan struct{})
	go progress(ctx, stats, done)

	// Walk the stages. New viewers needed for a stage are spawned at its start
	// (spread over the ramp window) and kept running for the rest of the test,
	// so load is cumulative.
	rng := rand.New(rand.NewSource(1))
	spawned := 0
	for idx, st := range cfg.stages {
		stats.curStage.Store(int32(idx))
		stageStart := time.Now()
		need := st.viewers - spawned
		if need < 0 {
			need = 0 // a stage with fewer viewers than the prior one just holds existing connections
		}
		rampWin := cfg.ramp
		if rampWin > st.dur/2 {
			rampWin = st.dur / 2
		}
		for j := 0; j < need; j++ {
			delay := time.Duration(0)
			if rampWin > 0 {
				delay = time.Duration(rng.Int63n(int64(rampWin) + 1))
			}
			wg.Add(1)
			go func(start time.Duration) {
				defer wg.Done()
				select {
				case <-time.After(start):
				case <-ctx.Done():
					return
				}
				runViewer(ctx, cfg, viewerClient, stats)
			}(delay)
		}
		spawned += need

		// Hold for the remainder of the stage.
		remain := st.dur - time.Since(stageStart)
		if remain > 0 {
			select {
			case <-time.After(remain):
			case <-ctx.Done():
			}
		}
		stats.stages[idx].connectedAtEnd = stats.viewerConnected.Load()
		stats.stages[idx].targetViewers = st.viewers
	}

	cancel() // stages done: drop all viewer streams and stop editors
	wg.Wait()
	close(done)

	report := stats.finalize(cfg, total)
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
		stats.stage().viewerFailed.Add(1)
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			stats.stage().viewerFailed.Add(1)
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		stats.stage().viewerFailed.Add(1)
		return
	}
	stats.viewerConnected.Add(1)
	defer stats.viewerConnected.Add(-1)

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

type stageStat struct {
	targetViewers  int
	connectedAtEnd int64

	editsOK    atomic.Int64
	editsBusy  atomic.Int64 // 5xx — the signature of write-lock/contention failures
	editsOther atomic.Int64 // non-2xx, non-5xx (auth, 404, etc.)
	editsErr   atomic.Int64 // transport error / timeout (status 0)

	eventsReceived atomic.Int64
	markersSeen    atomic.Int64
	viewerFailed   atomic.Int64

	mu     sync.Mutex
	editMS []float64
	propMS []float64
}

type stats struct {
	editSeq         atomic.Int64
	viewerConnected atomic.Int64
	viewerDropped   atomic.Int64

	stages    []*stageStat
	curStage  atomic.Int32
	startTime time.Time
}

func newStats(cfgStages []stageCfg) *stats {
	s := &stats{startTime: time.Now()}
	for range cfgStages {
		s.stages = append(s.stages, &stageStat{})
	}
	return s
}

func (s *stats) stage() *stageStat { return s.stages[s.curStage.Load()] }

func (s *stats) recordEdit(elapsed time.Duration, status int) {
	st := s.stage()
	switch {
	case status == 0:
		st.editsErr.Add(1)
	case status >= 200 && status < 300:
		st.editsOK.Add(1)
	case status >= 500:
		st.editsBusy.Add(1)
	default:
		st.editsOther.Add(1)
	}
	st.mu.Lock()
	st.editMS = append(st.editMS, float64(elapsed.Microseconds())/1000)
	st.mu.Unlock()
}

func (s *stats) recordEvent(data []byte) {
	st := s.stage()
	st.eventsReceived.Add(1)
	var env eventEnvelope
	if err := json.Unmarshal(data, &env); err != nil || len(env.Data) == 0 {
		return
	}
	var m marker
	if err := json.Unmarshal(env.Data, &m); err != nil || m.TSNano == 0 {
		return
	}
	st.markersSeen.Add(1)
	latency := float64(time.Now().UnixNano()-m.TSNano) / 1e6
	st.mu.Lock()
	st.propMS = append(st.propMS, latency)
	st.mu.Unlock()
}

type stageReport struct {
	Viewers        int     `json:"target_viewers"`
	ConnectedAtEnd int64   `json:"connected_at_end"`
	ViewersFailed  int64   `json:"viewers_failed"`
	EditsOK        int64   `json:"edits_ok"`
	Edits5xx       int64   `json:"edits_5xx_busy"`
	EditsOther     int64   `json:"edits_other"`
	EditsErr       int64   `json:"edits_transport_err"`
	EditP50        float64 `json:"edit_ms_p50"`
	EditP95        float64 `json:"edit_ms_p95"`
	EditMax        float64 `json:"edit_ms_max"`
	EventsReceived int64   `json:"sse_events_received"`
	MarkersSeen    int64   `json:"sse_markers_seen"`
	DeliveryRatio  float64 `json:"delivery_ratio"`
	PropP50        float64 `json:"propagation_ms_p50"`
	PropP95        float64 `json:"propagation_ms_p95"`
	PropP99        float64 `json:"propagation_ms_p99"`
	PropMax        float64 `json:"propagation_ms_max"`
}

type report struct {
	DurationSec float64       `json:"duration_sec"`
	Editors     int           `json:"editors"`
	Stages      []stageReport `json:"stages"`
}

func (s *stats) finalize(cfg config, total time.Duration) report {
	r := report{DurationSec: total.Seconds(), Editors: cfg.editors}
	for _, st := range s.stages {
		st.mu.Lock()
		editP50, editP95, _, editMax := percentiles(st.editMS)
		propP50, propP95, propP99, propMax := percentiles(st.propMS)
		st.mu.Unlock()

		okEdits := st.editsOK.Load()
		expected := okEdits * st.connectedAtEnd
		ratio := 0.0
		if expected > 0 {
			ratio = float64(st.markersSeen.Load()) / float64(expected)
		}
		r.Stages = append(r.Stages, stageReport{
			Viewers:        st.targetViewers,
			ConnectedAtEnd: st.connectedAtEnd,
			ViewersFailed:  st.viewerFailed.Load(),
			EditsOK:        okEdits,
			Edits5xx:       st.editsBusy.Load(),
			EditsOther:     st.editsOther.Load(),
			EditsErr:       st.editsErr.Load(),
			EditP50:        editP50,
			EditP95:        editP95,
			EditMax:        editMax,
			EventsReceived: st.eventsReceived.Load(),
			MarkersSeen:    st.markersSeen.Load(),
			DeliveryRatio:  ratio,
			PropP50:        propP50,
			PropP95:        propP95,
			PropP99:        propP99,
			PropMax:        propMax,
		})
	}
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
	fmt.Printf("\n================ load test report (%.0fs, %d editors) ================\n", r.DurationSec, r.Editors)
	fmt.Printf("%-8s %-9s %-7s %-22s %-16s %s\n",
		"viewers", "conn/fail", "edits", "edit_ms(p50/p95/max)", "deliver_ratio", "prop_ms(p50/p95/p99/max)")
	for _, s := range r.Stages {
		fmt.Printf("%-8d %3d/%-5d %3dok/%-3d5xx %6.0f /%6.0f /%6.0f   %-16.3f %5.0f /%5.0f /%5.0f /%5.0f\n",
			s.Viewers, s.ConnectedAtEnd, s.ViewersFailed,
			s.EditsOK, s.Edits5xx,
			s.EditP50, s.EditP95, s.EditMax,
			s.DeliveryRatio,
			s.PropP50, s.PropP95, s.PropP99, s.PropMax)
	}
	fmt.Println("==============================================================================")
	fmt.Println("conn = SSE viewers connected at stage end; deliver_ratio = markers_seen / (edits_ok * conn).")
	fmt.Println("Watch for: viewers_failed > 0 (nginx ceiling), 5xx > 0 (write contention), prop p95 climbing.")
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
			st := s.stage()
			fmt.Printf("\r  stage=%d conn=%d edits_ok=%d 5xx=%d events=%d   ",
				s.curStage.Load(), s.viewerConnected.Load(),
				st.editsOK.Load(), st.editsBusy.Load(), st.eventsReceived.Load())
		case <-ctx.Done():
			return
		case <-done:
			fmt.Print("\r")
			return
		}
	}
}
