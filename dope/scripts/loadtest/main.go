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
	"strconv"
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
	ekEditors    int
	ekMatches    []string
	editMode     string
	settle       time.Duration
}

// eventEnvelope mirrors the server's SSE payload wrapper (db.go eventEnvelope).
// A full-state broadcast carries `data`; a delta carries `ops` instead, so both
// have to be read to find an editor's marker.
type eventEnvelope struct {
	Scope    string          `json:"scope"`
	Revision int64           `json:"revision"`
	Seq      uint64          `json:"seq"`
	PrevSeq  uint64          `json:"prevSeq"`
	Data     json.RawMessage `json:"data"`
	Ops      []envelopeOp    `json:"ops"`
}

type envelopeOp struct {
	Path  []json.RawMessage `json:"path"`
	Value json.RawMessage   `json:"value"`
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
	var tokens, stages, ekMatches string
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
	flag.DurationVar(&cfg.settle, "settle", 3*time.Second, "stop editors this long before the run ends, so in-flight broadcasts can land while viewers still listen")
	flag.StringVar(&cfg.editMode, "edit-mode", "patch", "how flat-game editors write: patch (set-ops, the real client path) or put (whole state)")
	flag.IntVar(&cfg.ekEditors, "ek-editors", 0, "concurrent EK editors PATCHing per-match state ops (spread over -ek-matches)")
	flag.StringVar(&ekMatches, "ek-matches", "", "comma-separated EK match codes the EK editors mark cells in")
	flag.Parse()

	if cfg.festID == "" {
		cfg.festID = cfg.festRef
	}
	for _, t := range strings.Split(tokens, ",") {
		if t = strings.TrimSpace(t); t != "" {
			cfg.tokens = append(cfg.tokens, t)
		}
	}
	for _, code := range strings.Split(ekMatches, ",") {
		if code = strings.TrimSpace(code); code != "" {
			cfg.ekMatches = append(cfg.ekMatches, code)
		}
	}
	if cfg.editMode != "patch" && cfg.editMode != "put" {
		fmt.Fprintln(os.Stderr, "loadtest: -edit-mode must be patch or put")
		os.Exit(2)
	}
	if cfg.ekEditors > 0 && len(cfg.ekMatches) == 0 {
		fmt.Fprintln(os.Stderr, "loadtest: -ek-matches is required when -ek-editors > 0")
		os.Exit(2)
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
	if (cfg.editors > 0 || cfg.ekEditors > 0) && len(cfg.tokens) == 0 {
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
	// Editors stop before the viewers do: an edit made in the last instants of a
	// run has no chance to be delivered, and counting it would understate
	// delivery exactly as the ramp used to overstate latency.
	editCtx, stopEditors := context.WithCancel(ctx)
	defer stopEditors()
	if cfg.settle > 0 && cfg.settle < total {
		go func() {
			select {
			case <-time.After(total - cfg.settle):
				stopEditors()
			case <-ctx.Done():
			}
		}()
	}

	stats := newStats(cfg.stages)
	var wg sync.WaitGroup

	viewerClient := newHTTPClient(cfg, 0, maxViewers+cfg.editors+cfg.ekEditors+10)
	editorClient := newHTTPClient(cfg, 30*time.Second, cfg.editors+cfg.ekEditors+4)

	// Editors run for the whole test, attributing each edit to whatever stage
	// is current when it completes.
	for i := 0; i < cfg.editors; i++ {
		token := cfg.tokens[i%len(cfg.tokens)]
		wg.Add(1)
		go func(id int, tok string) {
			defer wg.Done()
			runEditor(editCtx, cfg, editorClient, stats, id, tok, rand.New(rand.NewSource(int64(id)+1)))
		}(i, token)
	}

	// EK editors drive the per-match state PATCH — the path the whole tournament
	// runs on — one per match code, round-robined when there are more editors.
	for i := 0; i < cfg.ekEditors; i++ {
		token := cfg.tokens[i%len(cfg.tokens)]
		code := cfg.ekMatches[i%len(cfg.ekMatches)]
		wg.Add(1)
		go func(id int, tok, code string) {
			defer wg.Done()
			runEKEditor(editCtx, cfg, editorClient, stats, id, tok, code, rand.New(rand.NewSource(int64(id)+1001)))
		}(i, token, code)
	}

	fmt.Printf("running: %d flat + %d EK editors, %d stages, %s total, target %s\n",
		cfg.editors, cfg.ekEditors, len(cfg.stages), total, cfg.base)
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

	var feed []scopedEvent
	lastSeq := map[string]uint64{}
	connectedAt := time.Now()
	defer func() { stats.recordViewerFeed(viewerFeed{connectedAt: connectedAt, arrivals: feed}) }()

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
				stats.recordEvent(dataBuf.Bytes(), &feed, lastSeq)
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
		m := marker{Seq: seq, TSNano: time.Now().UnixNano(), Editor: id}
		method, body := http.MethodPut, buildPayload(m, pad)
		if cfg.editMode == "patch" {
			method, body = http.MethodPatch, buildPatch(m, pad)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
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

// runEKEditor drives the per-match Protocol write path (ADR-0005): every edit is
// a blob-path set-op PATCH, exactly what a host's keyboard produces. It reads the
// match once to learn its team ids — the client speaks ids, never names — then
// marks a rotating cell, alternating right/blank so the state actually changes.
func runEKEditor(ctx context.Context, cfg config, client *http.Client, stats *stats, id int, token, code string, rng *rand.Rand) {
	base := strings.TrimRight(cfg.base, "/")
	matchURL := fmt.Sprintf("%s/api/fest/%s/games/%s/matches/%s", base, cfg.festRef, cfg.gameID, code)
	teamIDs, err := loadMatchTeamIDs(ctx, client, matchURL, token)
	if err != nil || len(teamIDs) == 0 {
		fmt.Fprintf(os.Stderr, "loadtest: EK editor %d cannot read match %s: %v\n", id, code, err)
		return
	}
	scope := fmt.Sprintf("match:%s:%s", cfg.gameID, code)
	marks := [2]string{"right", ""}
	for n := 0; ; n++ {
		jitter := time.Duration(rng.Int63n(int64(cfg.editInterval)/2+1)) - cfg.editInterval/4
		select {
		case <-time.After(cfg.editInterval + jitter):
		case <-ctx.Done():
			return
		}

		team := teamIDs[rng.Intn(len(teamIDs))]
		body, _ := json.Marshal(map[string]any{"ops": []any{map[string]any{
			"path":  []any{"teams", strconv.FormatInt(team, 10), "themes", rng.Intn(12), "answers", rng.Intn(5)},
			"value": marks[n%2],
		}}})
		req, err := http.NewRequestWithContext(ctx, http.MethodPatch, matchURL+"/state", bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", "session="+token)

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
		payload, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		stats.recordEdit(elapsed, resp.StatusCode)
		if resp.StatusCode/100 != 2 {
			continue
		}
		// The response carries the revision this edit committed at; viewers see
		// the same revision on the broadcast, which is what correlates the two.
		var view struct {
			Revision int64 `json:"revision"`
		}
		if json.Unmarshal(payload, &view) == nil && view.Revision > 0 {
			stats.recordSend(scope, view.Revision, start)
		}
	}
}

func loadMatchTeamIDs(ctx context.Context, client *http.Client, matchURL, token string) ([]int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, matchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", "session="+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var view struct {
		Teams []struct {
			ID int64 `json:"id"`
		} `json:"teams"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		return nil, err
	}
	var ids []int64
	for _, team := range view.Teams {
		if team.ID != 0 {
			ids = append(ids, team.ID)
		}
	}
	return ids, nil
}

// buildPatch writes the same marker fields as buildPayload, but as the set-ops
// a real od/KSI client sends — so the batcher's flat path is what is measured.
func buildPatch(m marker, pad string) []byte {
	op := func(key string, value any) map[string]any {
		return map[string]any{"path": []any{key}, "value": value}
	}
	// Each editor stamps its OWN path: a window folds co-editors' ops into one
	// merged delta, so a shared marker path would leave only the last writer's
	// timestamp and undercount delivery by design.
	b, _ := json.Marshal(map[string]any{"ops": []any{
		map[string]any{"path": []any{"_lt", strconv.Itoa(m.Editor)}, "value": m.TSNano},
		op("_lt_seq", m.Seq),
		op("pad", pad),
	}})
	return b
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
	// seqGaps counts deltas whose prevSeq does not chain onto the last one this
	// viewer saw for that scope — i.e. the server dropped an event for it. A real
	// client resyncs here; the driver just keeps reading, so a gap inflates the
	// measured propagation tail by however long the next broadcast takes.
	seqGaps atomic.Int64

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

	// EK edits carry no in-band marker: their broadcast is a server-computed
	// MatchView, not the editor's payload. Correlation is by revision instead —
	// the editor's response and the broadcast name the same one. Viewer-side
	// deltas coalesce, so a merged broadcast at revision R proves every edit up
	// to R reached that viewer; the join below is therefore "first arrival whose
	// revision is at least the edit's", per viewer, computed at the end so a
	// broadcast that beats its own HTTP response still counts.
	revMu       sync.Mutex
	sends       []scopedEvent
	viewerFeeds []viewerFeed
}

// viewerFeed is one viewer's match-scope arrivals plus when it connected. An
// edit made before a viewer connected was never that viewer's to receive, so
// the join must not charge it — otherwise every edit during the ramp measures
// as seconds of "latency" against every viewer still connecting.
type viewerFeed struct {
	connectedAt time.Time
	arrivals    []scopedEvent
}

// scopedEvent is one edit sent, or one broadcast received, on a match scope.
type scopedEvent struct {
	scope    string
	revision int64
	at       time.Time
}

// eventTrackLimit bounds the correlation slices so a long soak can't grow
// unbounded; overflow is logged as dropped rather than silently truncated.
const eventTrackLimit = 400_000

func (s *stats) recordSend(scope string, revision int64, at time.Time) {
	s.revMu.Lock()
	defer s.revMu.Unlock()
	if len(s.sends) < eventTrackLimit {
		s.sends = append(s.sends, scopedEvent{scope, revision, at})
	}
}

func (s *stats) recordViewerFeed(feed viewerFeed) {
	if len(feed.arrivals) == 0 {
		return
	}
	s.revMu.Lock()
	defer s.revMu.Unlock()
	s.viewerFeeds = append(s.viewerFeeds, feed)
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

// recordEvent classifies one SSE envelope. Match-scope envelopes append to the
// caller's own feed — delta envelopes carry `ops` and no `data`, so this must
// happen before the marker gate below drops them.
func (s *stats) recordEvent(data []byte, feed *[]scopedEvent, lastSeq map[string]uint64) {
	st := s.stage()
	st.eventsReceived.Add(1)
	var env eventEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return
	}
	if env.PrevSeq > 0 {
		if last, seen := lastSeq[env.Scope]; seen && last != env.PrevSeq {
			st.seqGaps.Add(1)
		}
	}
	if env.Seq > 0 {
		lastSeq[env.Scope] = env.Seq
	}
	if strings.HasPrefix(env.Scope, "match:") && env.Revision > 0 && len(*feed) < eventTrackLimit {
		*feed = append(*feed, scopedEvent{env.Scope, env.Revision, time.Now()})
	}
	markers := envelopeMarkers(env)
	if len(markers) == 0 {
		return
	}
	now := time.Now().UnixNano()
	st.markersSeen.Add(int64(len(markers)))
	st.mu.Lock()
	for _, m := range markers {
		st.propMS = append(st.propMS, float64(now-m.TSNano)/1e6)
	}
	st.mu.Unlock()
}

// envelopeMarkers finds every editor marker a broadcast carries: at the top
// level of a full-state payload, or as the per-editor `_lt/<id>` set-ops of a
// delta. A merged window carries one op per co-editor, and each one is a
// delivery, so all of them count.
func envelopeMarkers(env eventEnvelope) []marker {
	var out []marker
	if len(env.Data) > 0 {
		var m marker
		if err := json.Unmarshal(env.Data, &m); err == nil && m.TSNano != 0 {
			out = append(out, m)
		}
	}
	for _, op := range env.Ops {
		if len(op.Path) != 2 {
			continue
		}
		var key string
		if json.Unmarshal(op.Path[0], &key) != nil || key != "_lt" {
			continue
		}
		var m marker
		if json.Unmarshal(op.Value, &m.TSNano) == nil && m.TSNano != 0 {
			out = append(out, m)
		}
	}
	return out
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
	SeqGaps        int64   `json:"sse_seq_gaps"`
	MarkersSeen    int64   `json:"sse_markers_seen"`
	DeliveryRatio  float64 `json:"delivery_ratio"`
	PropP50        float64 `json:"propagation_ms_p50"`
	PropP95        float64 `json:"propagation_ms_p95"`
	PropP99        float64 `json:"propagation_ms_p99"`
	PropMax        float64 `json:"propagation_ms_max"`
}

// ekReport covers the per-match Protocol write path, correlated by revision
// rather than by an in-band marker (see stats.revSends).
type ekReport struct {
	Editors       int     `json:"editors"`
	Edits         int64   `json:"edits"`
	DeliveryRatio float64 `json:"delivery_ratio"`
	PropP50       float64 `json:"propagation_ms_p50"`
	PropP95       float64 `json:"propagation_ms_p95"`
	PropP99       float64 `json:"propagation_ms_p99"`
	PropMax       float64 `json:"propagation_ms_max"`
}

type report struct {
	DurationSec float64       `json:"duration_sec"`
	Editors     int           `json:"editors"`
	Stages      []stageReport `json:"stages"`
	EK          *ekReport     `json:"ek,omitempty"`
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
			SeqGaps:        st.seqGaps.Load(),
			MarkersSeen:    st.markersSeen.Load(),
			DeliveryRatio:  ratio,
			PropP50:        propP50,
			PropP95:        propP95,
			PropP99:        propP99,
			PropMax:        propMax,
		})
	}
	if cfg.ekEditors > 0 {
		r.EK = s.finalizeEK(cfg)
	}
	return r
}

// finalizeEK joins each EK edit to the first broadcast that carried it to each
// viewer. Deltas coalesce, so an arrival at revision R proves every edit up to R
// reached that viewer — the join is therefore a merge over revision order, not
// an equality match.
func (s *stats) finalizeEK(cfg config) *ekReport {
	s.revMu.Lock()
	defer s.revMu.Unlock()

	sendsByScope := map[string][]scopedEvent{}
	for _, send := range s.sends {
		sendsByScope[send.scope] = append(sendsByScope[send.scope], send)
	}
	for _, sends := range sendsByScope {
		sort.Slice(sends, func(i, j int) bool { return sends[i].revision < sends[j].revision })
	}

	var latencies []float64
	var delivered, expected int64
	for _, feed := range s.viewerFeeds {
		arrivalsByScope := map[string][]scopedEvent{}
		for _, arrival := range feed.arrivals {
			arrivalsByScope[arrival.scope] = append(arrivalsByScope[arrival.scope], arrival)
		}
		for scope, sends := range sendsByScope {
			arrivals := arrivalsByScope[scope]
			sort.Slice(arrivals, func(i, j int) bool { return arrivals[i].revision < arrivals[j].revision })
			next := 0
			for _, send := range sends {
				for next < len(arrivals) && arrivals[next].revision < send.revision {
					next++
				}
				if send.at.Before(feed.connectedAt) {
					continue // this viewer was not watching yet
				}
				expected++
				if next == len(arrivals) {
					break // this viewer never saw a broadcast at or past this edit
				}
				delivered++
				if ms := float64(arrivals[next].at.Sub(send.at)) / 1e6; ms >= 0 {
					latencies = append(latencies, ms)
				}
			}
		}
	}

	p50, p95, p99, max := percentiles(latencies)
	ratio := 0.0
	if expected > 0 {
		ratio = float64(delivered) / float64(expected)
	}
	return &ekReport{
		Editors: cfg.ekEditors, Edits: int64(len(s.sends)), DeliveryRatio: ratio,
		PropP50: p50, PropP95: p95, PropP99: p99, PropMax: max,
	}
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
	for _, st := range r.Stages {
		if st.SeqGaps > 0 {
			fmt.Printf("seq gaps (dropped events, a real client would resync): %d over %d events\n", st.SeqGaps, st.EventsReceived)
		}
	}
	if r.EK != nil {
		fmt.Printf("\nEK match-state PATCH (%d editors): %d edits, delivery %.4f, prop_ms %.0f /%.0f /%.0f /%.0f\n",
			r.EK.Editors, r.EK.Edits, r.EK.DeliveryRatio, r.EK.PropP50, r.EK.PropP95, r.EK.PropP99, r.EK.PropMax)
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
