package dopeserver

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dope/dope/domain/games"
)

// Static mode ("DDoS lockdown") is a degradation layer for public viewer pages.
// Under load — or on demand via the /static URL suffix or the localhost control
// endpoint — viewer pages are served from a precomputed, in-memory HTML snapshot
// with NO SSE connection. Each request becomes a memory copy + socket write, and
// the pages become edge-cacheable. The live realtime path (editors, SSE deltas)
// is untouched; only anonymous viewers are degraded. See db.go (buildViewerInit/
// buildGameInit), main.go (handleEvents shedding) and pages_public.go (routing).

// staticEntry is a precomputed viewer-page snapshot: the spliced HTML (raw) and
// its gzip-compressed form, both served with zero per-request work. lastAccess
// (unix nanos) drives retention in the regen ticker.
type staticEntry struct {
	raw        []byte
	gz         []byte
	lastAccess atomic.Int64
}

// staticBuildCall is one in-flight snapshot build, shared by concurrent cache
// misses for the same route (a tiny hand-rolled singleflight — avoids pulling in
// golang.org/x/sync just for this).
type staticBuildCall struct {
	wg  sync.WaitGroup
	e   *staticEntry
	err error
}

type staticConfig struct {
	rateHigh  int64         // enter static mode above this many requests/sec
	rateLow   int64         // candidate auto-exit below this many requests/sec
	sseMax    int64         // enter static mode above this many viewer SSE connections
	cooldown  int           // seconds of min dwell AND sustained-low rate before auto-exit
	retention time.Duration // drop snapshots not accessed within this window
}

// liveFallthroughCap bounds how many cookie-bearing (candidate-editor) viewer
// requests may take the live (DB-hitting) render path concurrently while static
// mode is engaged. Beyond it, even cookie-bearing requests get the cached
// snapshot, so a flood of forged `session=...` cookies can't pierce the shield.
const liveFallthroughCap = 32

func envInt64(key string, def int64) int64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

// initStaticMode reads DOPE_STATIC_* config, applies the initial manual override,
// and launches the load-eval + snapshot-regen tickers plus the optional
// localhost-only control server. Call once from main() after assets are set.
func (s *server) initStaticMode() {
	s.staticCfg = staticConfig{
		rateHigh:  envInt64("DOPE_STATIC_RATE_HIGH", 400),
		rateLow:   envInt64("DOPE_STATIC_RATE_LOW", 150),
		sseMax:    envInt64("DOPE_STATIC_SSE_MAX", 1200),
		cooldown:  int(envInt64("DOPE_STATIC_COOLDOWN", 30)),
		retention: time.Duration(envInt64("DOPE_STATIC_RETENTION", 60)) * time.Second,
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DOPE_STATIC"))) {
	case "on":
		s.eng.StaticManual.Store(1)
		s.eng.StaticMode.Store(true)
	case "off":
		s.eng.StaticManual.Store(2)
	}
	go s.runStaticEval()
	go s.runStaticRegen()
	if addr := strings.TrimSpace(os.Getenv("DOPE_STATIC_CTL")); addr != "" {
		s.startStaticControl(addr)
	}
}

// runStaticEval drives staticMode from the load gauges once per second, honouring
// the manual override and applying hysteresis so the mode doesn't flap.
func (s *server) runStaticEval() {
	cfg := s.staticCfg
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var overTicks, underTicks, dwell int
	for range ticker.C {
		rate := s.eng.ReqRate.Swap(0) // requests in the last second
		s.eng.LastRate.Store(rate)
		sse := s.eng.SseConns.Load()

		switch s.eng.StaticManual.Load() {
		case 1:
			s.setStatic(true)
			continue
		case 2:
			s.setStatic(false)
			continue
		}

		if s.eng.StaticMode.Load() {
			dwell++
			if rate < cfg.rateLow {
				underTicks++
			} else {
				underTicks = 0
			}
			// Auto-exit is NOT keyed on sseConns: new SSE is shed in static mode so
			// the gauge sits near zero, which would make it flap on/off. Exit only
			// after a minimum dwell AND a sustained-low request rate. reqRate counts
			// the static pages' own self-reloads, so genuinely heavy traffic keeps
			// the shield up.
			if dwell >= cfg.cooldown && underTicks >= cfg.cooldown {
				s.setStatic(false)
				overTicks, underTicks, dwell = 0, 0, 0
			}
		} else {
			if rate > cfg.rateHigh || sse > cfg.sseMax {
				overTicks++
			} else {
				overTicks = 0
			}
			if overTicks >= 2 { // ~2s sustained, so a single blip doesn't trip it
				s.setStatic(true)
				overTicks, underTicks, dwell = 0, 0, 0
			}
		}
	}
}

// setStatic flips the effective mode and, on entry, sheds connected viewers via
// a lockdown sentinel. Logs every transition.
func (s *server) setStatic(on bool) {
	if s.eng.StaticMode.Swap(on) == on {
		return // no change
	}
	if on {
		log.Printf("static mode: ON (req/s=%d sse=%d)", s.eng.LastRate.Load(), s.eng.SseConns.Load())
		s.eng.RT.BroadcastLockdown()
	} else {
		log.Printf("static mode: OFF")
	}
}

// runStaticRegen refreshes hot snapshots every 5s and evicts cold ones. Bounding
// regeneration to recently-accessed routes caps DB/marshal work at (hot routes /
// 5s) no matter how hard the cache is hammered.
func (s *server) runStaticRegen() {
	cfg := s.staticCfg
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now().UnixNano()
		var hot, stale []hostInitRoute
		s.staticMu.RLock()
		for route, e := range s.staticCache {
			if now-e.lastAccess.Load() > cfg.retention.Nanoseconds() {
				stale = append(stale, route)
			} else {
				hot = append(hot, route)
			}
		}
		s.staticMu.RUnlock()

		for _, route := range hot {
			e, err := s.buildStaticEntry(context.Background(), route)
			if err != nil || e == nil {
				continue
			}
			s.staticMu.Lock()
			if old := s.staticCache[route]; old != nil {
				e.lastAccess.Store(old.lastAccess.Load())
			}
			s.staticCache[route] = e
			s.staticMu.Unlock()
		}
		if len(stale) > 0 {
			s.staticMu.Lock()
			for _, route := range stale {
				delete(s.staticCache, route)
			}
			s.staticMu.Unlock()
		}
	}
}

// buildStaticEntry renders one viewer-page snapshot for a route: it resolves the
// game type, reuses the existing init builders (with Static=true, CanEdit=false),
// splices into the shell, and precomputes both raw and gzipped bytes.
func (s *server) buildStaticEntry(ctx context.Context, route hostInitRoute) (*staticEntry, error) {
	var gameType string
	_ = s.eng.DB.QueryRowContext(ctx, `select game_type from games where id = ? and fest_id = ?`, route.GameID, route.FestID).Scan(&gameType)

	var htmlPath, marker string
	var data []byte
	if games.IsChGK(gameType) {
		htmlPath, marker = "static/od.html", gameInitMarker
		if gameType != games.OD {
			htmlPath = "static/si.html"
		}
		payload, err := s.buildGameInit(ctx, festScope{FestID: route.FestID, GameID: route.GameID})
		if err != nil {
			return nil, err
		}
		payload.Static = true
		payload.CanEdit = false
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		data = b
	} else {
		htmlPath, marker = "static/viewer.html", viewerInitMarker
		payload, err := s.buildViewerInit(ctx, route)
		if err != nil {
			return nil, err
		}
		payload.Static = true
		payload.CanEdit = false
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		data = b
	}

	html, err := s.renderInjectedBytes(htmlPath, marker, data)
	if err != nil {
		return nil, err
	}
	return &staticEntry{raw: html, gz: gzipBytes(html)}, nil
}

// renderInjectedBytes splices payload over the marker in an HTML shell and
// applies the asset cache-buster, returning the bytes (the no-ResponseWriter
// twin of serveInjectedHTML, so snapshots can be cached).
func (s *server) renderInjectedBytes(htmlPath, marker string, payload []byte) ([]byte, error) {
	body, err := fs.ReadFile(s.eng.Assets, htmlPath)
	if err != nil {
		return nil, err
	}
	idx := bytes.Index(body, []byte(marker))
	if idx < 0 {
		return nil, fmt.Errorf("static: marker %q not found in %s", marker, htmlPath)
	}
	out := make([]byte, 0, len(body)+len(payload))
	out = append(out, body[:idx]...)
	out = append(out, payload...)
	out = append(out, body[idx+len(marker):]...)
	return s.versionAssetRefs(out), nil
}

// gzipBytes compresses raw using the shared pool at BestSpeed (same level the
// live gzip middleware uses).
func gzipBytes(raw []byte) []byte {
	var buf bytes.Buffer
	gz := gzipPool.Get().(*gzip.Writer)
	gz.Reset(&buf)
	_, _ = gz.Write(raw)
	_ = gz.Close()
	gz.Reset(io.Discard)
	gzipPool.Put(gz)
	return buf.Bytes()
}

// staticSnapshot returns the cached snapshot for a route, building it once on a
// miss. Concurrent misses for the same route share a single build (singleflight).
func (s *server) staticSnapshot(ctx context.Context, route hostInitRoute) *staticEntry {
	s.staticMu.RLock()
	e := s.staticCache[route]
	s.staticMu.RUnlock()
	if e != nil {
		return e
	}

	s.staticMu.Lock()
	if e := s.staticCache[route]; e != nil {
		s.staticMu.Unlock()
		return e
	}
	if call := s.staticBuilds[route]; call != nil {
		s.staticMu.Unlock()
		call.wg.Wait()
		return call.e
	}
	call := &staticBuildCall{}
	call.wg.Add(1)
	if s.staticBuilds == nil {
		s.staticBuilds = make(map[hostInitRoute]*staticBuildCall)
	}
	s.staticBuilds[route] = call
	s.staticMu.Unlock()

	e, err := s.buildStaticEntry(ctx, route)
	call.e, call.err = e, err
	call.wg.Done()

	s.staticMu.Lock()
	delete(s.staticBuilds, route)
	if err == nil && e != nil {
		if s.staticCache == nil {
			s.staticCache = make(map[hostInitRoute]*staticEntry)
		}
		s.staticCache[route] = e
	}
	s.staticMu.Unlock()
	return e
}

// serveStaticSnapshot writes the cached snapshot for a route. It serves the
// pre-gzipped bytes directly and sets Content-Encoding itself, so the gzip
// middleware passes the response through untouched (no per-request gzip CPU).
func (s *server) serveStaticSnapshot(w http.ResponseWriter, r *http.Request, route hostInitRoute) {
	e := s.staticSnapshot(r.Context(), route)
	if e == nil {
		// Build failed (e.g. the game vanished mid-request); fall back to the live
		// viewer shell so the request still gets a usable page.
		s.serveViewerHTML(w, r)
		return
	}
	e.lastAccess.Store(time.Now().UnixNano())

	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	// A short max-age makes the snapshot edge-cacheable (the documented CDN layer)
	// without going stale beyond one refresh cycle; the page self-reloads on a ~5s
	// jitter regardless.
	h.Set("Cache-Control", "public, max-age=5")
	body := e.raw
	if acceptsGzip(r) && len(e.gz) > 0 {
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		body = e.gz
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// writeStaticStatus emits the current mode + load gauges as JSON for the control
// endpoint.
func (s *server) writeStaticStatus(w http.ResponseWriter) {
	manual := "auto"
	switch s.eng.StaticManual.Load() {
	case 1:
		manual = "on"
	case 2:
		manual = "off"
	}
	s.staticMu.RLock()
	cacheEntries := len(s.staticCache)
	s.staticMu.RUnlock()
	status := map[string]any{
		"static":          s.eng.StaticMode.Load(),
		"manual":          manual,
		"reqRate":         s.eng.LastRate.Load(),
		"sseConns":        s.eng.SseConns.Load(),
		"inFlight":        s.eng.InFlight.Load(),
		"liveFallthrough": s.eng.LiveFallthrough.Load(),
		"cacheEntries":    cacheEntries,
		"config": map[string]any{
			"rateHigh": s.staticCfg.rateHigh,
			"rateLow":  s.staticCfg.rateLow,
			"sseMax":   s.staticCfg.sseMax,
			"cooldown": s.staticCfg.cooldown,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// startStaticControl runs a localhost-only control server (modeled on the pprof
// listener) for flipping the manual override at runtime. There is no global-admin
// identity in the app, so this is intentionally bound to localhost — reachable
// only over SSH on the box, no public exposure.
func (s *server) startStaticControl(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/static", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("mode") {
		case "on":
			s.eng.StaticManual.Store(1)
		case "off":
			s.eng.StaticManual.Store(2)
		case "auto":
			s.eng.StaticManual.Store(0)
		case "":
			// status-only request
		default:
			http.Error(w, "mode must be on|off|auto", http.StatusBadRequest)
			return
		}
		s.writeStaticStatus(w)
	})
	mux.HandleFunc("/admin/status", func(w http.ResponseWriter, r *http.Request) {
		s.writeStaticStatus(w)
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Printf("static control listening on http://%s/admin/status", addr)
		if err := srv.ListenAndServe(); err != nil {
			log.Printf("static control server stopped: %v", err)
		}
	}()
}
