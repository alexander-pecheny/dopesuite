package main

import (
	"compress/gzip"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed static/*
var staticFiles embed.FS

const (
	stateFile                 = "match_state.json"
	themeCount                = 12
	ksiThemeCount             = 20
	actionAddShootoutTheme    = "addShootoutTheme"
	actionRemoveShootoutTheme = "removeShootoutTheme"
)

var questionValues = [5]int{10, 20, 30, 40, 50}

type ThemeEntry struct {
	Player  string    `json:"player"`
	Answers [5]string `json:"answers"`
}

type TeamState struct {
	Name           string       `json:"name"`
	Roster         []string     `json:"roster"`
	Themes         []ThemeEntry `json:"themes"`
	ShootoutThemes []ThemeEntry `json:"shootoutThemes,omitempty"`
	Tiebreak       int          `json:"tiebreak"`
	Place          float64      `json:"place"`
}

type MatchState struct {
	Title     string      `json:"title"`
	Finished  bool        `json:"finished"`
	Revision  int64       `json:"revision"`
	UpdatedAt time.Time   `json:"updatedAt"`
	Teams     []TeamState `json:"teams"`
}

type ThemeView struct {
	Player  string    `json:"player"`
	Answers [5]string `json:"answers"`
	Score   int       `json:"score"`
}

type TeamView struct {
	Name           string      `json:"name"`
	Roster         []string    `json:"roster"`
	Themes         []ThemeView `json:"themes"`
	ShootoutThemes []ThemeView `json:"shootoutThemes"`
	Total          int         `json:"total"`
	Place          float64     `json:"place"`
	Plus           int         `json:"plus"`
	ShootoutTotal  int         `json:"shootoutTotal"`
	Tiebreak       int         `json:"tiebreak"`
	CorrectCounts  [5]int      `json:"correctCounts"`
	WrongCounts    [5]int      `json:"wrongCounts"`
}

type StandingView struct {
	Name     string  `json:"name"`
	Place    float64 `json:"place"`
	Total    int     `json:"total"`
	Plus     int     `json:"plus"`
	Tiebreak int     `json:"tiebreak"`
}

type MatchView struct {
	Title          string         `json:"title"`
	Code           string         `json:"code,omitempty"`
	StageCode      string         `json:"stageCode,omitempty"`
	StageTitle     string         `json:"stageTitle,omitempty"`
	Venue          *VenueView     `json:"venue,omitempty"`
	Finished       bool           `json:"finished"`
	Revision       int64          `json:"revision"`
	UpdatedAt      string         `json:"updatedAt"`
	QuestionValues [5]int         `json:"questionValues"`
	Teams          []TeamView     `json:"teams"`
	Standings      []StandingView `json:"standings"`
}

type event struct {
	festID   int64
	revision int64
	data     []byte
}

type hostPresenceEvent struct {
	festID int64
	data   []byte
}

type server struct {
	mu              sync.RWMutex
	db              *sql.DB
	festID          int64
	activeGameID    int64
	activeMatchCode string
	state           MatchState
	// subMu guards the SSE subscriber maps independently of mu so that
	// DB write transactions (which take mu) don't block event fan-out.
	subMu           sync.RWMutex
	subscribers     map[int64]map[chan event]struct{}
	hostSubscribers map[int64]map[chan hostPresenceEvent]struct{}
	assets          fs.FS
	assetNoCache    bool
	sendTelegram    telegramSender
	// festViewCache holds JSON-marshaled FestView responses keyed by
	// (festID, gameID). Invalidated wholesale per fest on broadcastState,
	// since any of the data folded into FestView (venues, stages, matches)
	// may have changed.
	festViewMu    sync.RWMutex
	festViewCache map[int64]map[int64][]byte
}

type updateRequest struct {
	Team     int      `json:"team"`
	Action   string   `json:"action,omitempty"`
	Finished *bool    `json:"finished,omitempty"`
	Theme    *int     `json:"theme,omitempty"`
	Shootout *bool    `json:"shootout,omitempty"`
	Answer   *int     `json:"answer,omitempty"`
	Mark     *string  `json:"mark,omitempty"`
	Player   *string  `json:"player,omitempty"`
	Tiebreak *int     `json:"tiebreak,omitempty"`
	Place    *float64 `json:"place,omitempty"`
}

func main() {
	srv, err := newServer()
	if err != nil {
		log.Fatal(err)
	}
	assets, assetMode := staticSource()
	noCacheAssets := assetMode == "disk"
	srv.assets = assets
	srv.assetNoCache = noCacheAssets

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handlePublicIndex)
	mux.HandleFunc("/fest/", srv.handleFestRouter)
	mux.HandleFunc("/register", srv.handleRegisterPage)
	mux.HandleFunc("/register/invite", srv.handleRegisterInviteSubmit)
	mux.HandleFunc("/register/username", srv.handleRegisterUsernameSubmit)
	mux.HandleFunc("/login", srv.serveStaticPage(assets, "static/login.html", noCacheAssets))
	mux.HandleFunc("/profile", srv.handleProfilePage)
	mux.HandleFunc("/profile/logout", srv.handleProfileLogout)
	mux.HandleFunc("/api/import", srv.handleImport)
	mux.HandleFunc("/host", srv.handleHostLanding)
	mux.HandleFunc("/host/", srv.handleHostRouter)
	mux.HandleFunc("/api/fest/", srv.handleScopedAPI)
	mux.HandleFunc("/api/auth/register/start", srv.handleAuthRegisterStart)
	mux.HandleFunc("/api/auth/register/status", srv.handleAuthRegisterStatus)
	mux.HandleFunc("/api/auth/login/start", srv.handleAuthLoginStart)
	mux.HandleFunc("/api/auth/login", srv.handleAuthLogin)
	mux.HandleFunc("/api/auth/login-password", srv.handleAuthLoginPassword)
	mux.HandleFunc("/api/auth/logout", srv.handleAuthLogout)
	mux.HandleFunc("/api/auth/me", srv.handleAuthMe)
	mux.HandleFunc("/api/auth/username", srv.handleAuthUsername)
	mux.HandleFunc("/events", srv.handleEvents)
	mux.HandleFunc("/host-events", srv.handleHostEvents)
	mux.Handle("/static/", staticFileServer(assets, noCacheAssets))

	port := strings.TrimPrefix(os.Getenv("PORT"), ":")
	if port == "" {
		port = "9672"
	}
	addr := ":" + port
	log.Printf("serving static from %s", assetMode)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("bind %s: %v", addr, err)
	}
	log.Printf("listening on http://localhost%s/host and http://localhost%s/", addr, addr)
	httpSrv := &http.Server{
		Handler:           gzipMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		// No WriteTimeout: SSE responses are intentionally long-lived.
		IdleTimeout: 120 * time.Second,
	}
	log.Fatal(httpSrv.Serve(listener))
}

func staticSource() (fs.FS, string) {
	if info, err := os.Stat("static"); err == nil && info.IsDir() {
		return os.DirFS("."), "disk"
	}
	if info, err := os.Stat("dope/static"); err == nil && info.IsDir() {
		return os.DirFS("dope"), "disk"
	}
	return staticFiles, "embed"
}

func staticFileServer(source fs.FS, noCache bool) http.Handler {
	handler := http.FileServer(http.FS(source))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case noCache:
			w.Header().Set("Cache-Control", "no-cache")
		case strings.HasPrefix(r.URL.Path, "/static/fonts/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		default:
			// In embed mode the asset bytes are baked into the binary
			// and only change when a new build is deployed. Letting
			// browsers skip revalidation for a few minutes makes page
			// loads dramatically cheaper at scale.
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		handler.ServeHTTP(w, r)
	})
}

// gzipPool recycles gzip.Writer instances. Each writer holds a ~64KB internal
// buffer, so pooling cuts allocations sharply when hundreds of viewers fetch
// JSON at once.
var gzipPool = sync.Pool{
	New: func() any {
		// BestSpeed is the sweet spot for low-CPU VPS hosting: still ~3x
		// shrink on JSON, a fraction of the CPU of DefaultCompression.
		gz, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return gz
	},
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gz           *gzip.Writer
	headerSent   bool
	bypass       bool // true means write straight through, never gzip
	statusCached int
}

func newGzipResponseWriter(w http.ResponseWriter) *gzipResponseWriter {
	return &gzipResponseWriter{ResponseWriter: w}
}

func (g *gzipResponseWriter) WriteHeader(status int) {
	if g.headerSent {
		return
	}
	g.headerSent = true
	g.statusCached = status
	h := g.Header()
	if !shouldGzipResponse(status, h) {
		g.bypass = true
		g.ResponseWriter.WriteHeader(status)
		return
	}
	h.Set("Content-Encoding", "gzip")
	h.Add("Vary", "Accept-Encoding")
	// Content-Length set by the inner handler is no longer accurate.
	h.Del("Content-Length")
	gz := gzipPool.Get().(*gzip.Writer)
	gz.Reset(g.ResponseWriter)
	g.gz = gz
	g.ResponseWriter.WriteHeader(status)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.headerSent {
		if g.Header().Get("Content-Type") == "" {
			g.Header().Set("Content-Type", http.DetectContentType(b))
		}
		g.WriteHeader(http.StatusOK)
	}
	if g.bypass || g.gz == nil {
		return g.ResponseWriter.Write(b)
	}
	return g.gz.Write(b)
}

func (g *gzipResponseWriter) Flush() {
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *gzipResponseWriter) finish() {
	if g.gz == nil {
		return
	}
	_ = g.gz.Close()
	g.gz.Reset(io.Discard)
	gzipPool.Put(g.gz)
	g.gz = nil
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// SSE responses are flushed incrementally; gzip would buffer
		// them and break the framing the client expects.
		if r.URL.Path == "/events" || r.URL.Path == "/host-events" {
			next.ServeHTTP(w, r)
			return
		}
		if !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		gw := newGzipResponseWriter(w)
		defer gw.finish()
		next.ServeHTTP(gw, r)
	})
}

func acceptsGzip(r *http.Request) bool {
	enc := r.Header.Get("Accept-Encoding")
	if enc == "" {
		return false
	}
	for _, v := range strings.Split(enc, ",") {
		token := strings.TrimSpace(v)
		if i := strings.IndexByte(token, ';'); i >= 0 {
			token = token[:i]
		}
		if strings.EqualFold(token, "gzip") {
			return true
		}
	}
	return false
}

func shouldGzipResponse(status int, h http.Header) bool {
	if status < 200 || status == 204 || status == 304 {
		return false
	}
	if h.Get("Content-Encoding") != "" {
		return false
	}
	return isGzippableType(h.Get("Content-Type"))
}

func isGzippableType(ct string) bool {
	if ct == "" {
		// We don't know yet — let it through; http.DetectContentType will
		// pick a text-ish type for most handler payloads.
		return true
	}
	base := ct
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		base = ct[:i]
	}
	base = strings.TrimSpace(strings.ToLower(base))
	if base == "text/event-stream" {
		return false
	}
	switch {
	case strings.HasPrefix(base, "text/"):
		return true
	case base == "application/json":
		return true
	case base == "application/javascript":
		return true
	case base == "application/xml":
		return true
	case base == "image/svg+xml":
		return true
	}
	return false
}

func newServer() (*server, error) {
	dbPath := os.Getenv("DOPE_DB")
	if dbPath == "" {
		dbPath = dbFile
	}
	db, err := openFestDB(dbPath)
	if err != nil {
		return nil, err
	}
	festID, gameID, matchCode, err := loadActiveContext(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if matchCode == "" {
		matchCode = defaultMatchCode
	}
	return &server{
		db:              db,
		festID:          festID,
		activeGameID:    gameID,
		activeMatchCode: matchCode,
		subscribers:     make(map[int64]map[chan event]struct{}),
		hostSubscribers: make(map[int64]map[chan hostPresenceEvent]struct{}),
	}, nil
}

func loadState(path string) (MatchState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		state := defaultMatch()
		normalizeState(&state)
		return state, nil
	}
	if err != nil {
		return MatchState{}, err
	}
	var state MatchState
	if err := json.Unmarshal(data, &state); err != nil {
		return MatchState{}, fmt.Errorf("read %s: %w", path, err)
	}
	normalizeState(&state)
	return state, nil
}

func saveState(path string, state MatchState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func normalizeState(state *MatchState) {
	if state.Title == "" {
		state.Title = "Бой A"
	}
	if state.Revision == 0 {
		state.Revision = 1
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now()
	}
	shootoutThemeCount := 0
	for i := range state.Teams {
		if len(state.Teams[i].ShootoutThemes) > shootoutThemeCount {
			shootoutThemeCount = len(state.Teams[i].ShootoutThemes)
		}
	}
	for i := range state.Teams {
		state.Teams[i].Tiebreak = 0
		if len(state.Teams[i].Themes) < themeCount {
			missing := themeCount - len(state.Teams[i].Themes)
			state.Teams[i].Themes = append(state.Teams[i].Themes, make([]ThemeEntry, missing)...)
		}
		if len(state.Teams[i].Themes) > themeCount {
			state.Teams[i].Themes = state.Teams[i].Themes[:themeCount]
		}
		for t := range state.Teams[i].Themes {
			for a := range state.Teams[i].Themes[t].Answers {
				state.Teams[i].Themes[t].Answers[a] = normalizeMark(state.Teams[i].Themes[t].Answers[a])
			}
		}
		if len(state.Teams[i].ShootoutThemes) < shootoutThemeCount {
			missing := shootoutThemeCount - len(state.Teams[i].ShootoutThemes)
			state.Teams[i].ShootoutThemes = append(state.Teams[i].ShootoutThemes, make([]ThemeEntry, missing)...)
		}
		for t := range state.Teams[i].ShootoutThemes {
			for a := range state.Teams[i].ShootoutThemes[t].Answers {
				state.Teams[i].ShootoutThemes[t].Answers[a] = normalizeMark(state.Teams[i].ShootoutThemes[t].Answers[a])
			}
		}
	}
}

func (s *server) serveStaticPage(source fs.FS, path string, noCache bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if noCache {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeFileFS(w, r, source, path)
	}
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	festID, err := resolveFestID(r.Context(), s.db, strings.TrimSpace(r.URL.Query().Get("fest_id")))
	if err != nil || festID <= 0 {
		http.Error(w, "missing fest_id", http.StatusBadRequest)
		return
	}
	if !s.authorizeFestRead(w, r, festID) {
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch := make(chan event, 8)
	s.addSubscriber(festID, ch)
	defer s.removeSubscriber(festID, ch)

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case ev := <-ch:
			writeSSE(w, "state", ev.revision, ev.data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *server) handleHostEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	festID, err := resolveFestID(r.Context(), s.db, strings.TrimSpace(r.URL.Query().Get("fest_id")))
	if err != nil || festID <= 0 {
		http.Error(w, "missing fest_id", http.StatusBadRequest)
		return
	}
	if !s.authorizeHostPresence(w, r, festID) {
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch := make(chan hostPresenceEvent, 16)
	s.addHostSubscriber(festID, ch)
	defer s.removeHostSubscriber(festID, ch)

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case ev := <-ch:
			writeSSE(w, "presence", 0, ev.data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *server) addSubscriber(festID int64, ch chan event) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	if s.subscribers == nil {
		s.subscribers = make(map[int64]map[chan event]struct{})
	}
	bucket, ok := s.subscribers[festID]
	if !ok {
		bucket = make(map[chan event]struct{})
		s.subscribers[festID] = bucket
	}
	bucket[ch] = struct{}{}
}

func (s *server) removeSubscriber(festID int64, ch chan event) {
	s.subMu.Lock()
	if bucket, ok := s.subscribers[festID]; ok {
		delete(bucket, ch)
		if len(bucket) == 0 {
			delete(s.subscribers, festID)
		}
	}
	s.subMu.Unlock()
	close(ch)
}

func (s *server) broadcast(ev event) {
	// Snapshot the channel list under the read lock so the slow send
	// path runs without keeping other broadcasts or subscriber churn
	// blocked.
	var chs []chan event
	s.subMu.RLock()
	if bucket, ok := s.subscribers[ev.festID]; ok && len(bucket) > 0 {
		chs = make([]chan event, 0, len(bucket))
		for ch := range bucket {
			chs = append(chs, ch)
		}
	}
	s.subMu.RUnlock()
	for _, ch := range chs {
		select {
		case ch <- ev:
		default:
			// Buffer is full; drop the oldest entry and try again so
			// late subscribers always see the latest state.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- ev:
			default:
			}
		}
	}
}

func (s *server) addHostSubscriber(festID int64, ch chan hostPresenceEvent) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	if s.hostSubscribers == nil {
		s.hostSubscribers = make(map[int64]map[chan hostPresenceEvent]struct{})
	}
	bucket, ok := s.hostSubscribers[festID]
	if !ok {
		bucket = make(map[chan hostPresenceEvent]struct{})
		s.hostSubscribers[festID] = bucket
	}
	bucket[ch] = struct{}{}
}

func (s *server) removeHostSubscriber(festID int64, ch chan hostPresenceEvent) {
	s.subMu.Lock()
	if bucket, ok := s.hostSubscribers[festID]; ok {
		delete(bucket, ch)
		if len(bucket) == 0 {
			delete(s.hostSubscribers, festID)
		}
	}
	s.subMu.Unlock()
	close(ch)
}

func (s *server) broadcastHostPresence(ev hostPresenceEvent) {
	var chs []chan hostPresenceEvent
	s.subMu.RLock()
	if bucket, ok := s.hostSubscribers[ev.festID]; ok && len(bucket) > 0 {
		chs = make([]chan hostPresenceEvent, 0, len(bucket))
		for ch := range bucket {
			chs = append(chs, ch)
		}
	}
	s.subMu.RUnlock()
	for _, ch := range chs {
		select {
		case ch <- ev:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- ev:
			default:
			}
		}
	}
}

func (s *server) applyUpdate(req updateRequest) (MatchView, []byte, error) {
	if s.db != nil {
		return s.applyMatchUpdate(s.festID, s.activeMatchCode, req)
	}
	return s.applyLegacyUpdate(req)
}

func (s *server) applyLegacyUpdate(req updateRequest) (MatchView, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Finished != nil {
		if hasMatchEdit(req) {
			return MatchView{}, nil, errors.New("finished update must be standalone")
		}
		s.state.Finished = *req.Finished
		if *req.Finished {
			assignComputedPlaces(&s.state)
		}
		return s.commitLocked()
	}
	if s.state.Finished {
		return MatchView{}, nil, errors.New("match is finished")
	}

	if req.Action != "" {
		if hasTeamEdit(req) {
			return MatchView{}, nil, errors.New("action update must be standalone")
		}
		switch req.Action {
		case actionAddShootoutTheme:
			for i := range s.state.Teams {
				s.state.Teams[i].ShootoutThemes = append(s.state.Teams[i].ShootoutThemes, ThemeEntry{})
			}
			return s.commitLocked()
		case actionRemoveShootoutTheme:
			if len(s.state.Teams) == 0 || len(s.state.Teams[0].ShootoutThemes) == 0 {
				return MatchView{}, nil, errors.New("no shootout themes to remove")
			}
			for i := range s.state.Teams {
				if len(s.state.Teams[i].ShootoutThemes) > 0 {
					last := len(s.state.Teams[i].ShootoutThemes) - 1
					s.state.Teams[i].ShootoutThemes = s.state.Teams[i].ShootoutThemes[:last]
				}
			}
			return s.commitLocked()
		default:
			return MatchView{}, nil, errors.New("bad action")
		}
	}

	if req.Team < 0 || req.Team >= len(s.state.Teams) {
		return MatchView{}, nil, errors.New("bad team index")
	}
	team := &s.state.Teams[req.Team]

	if req.Tiebreak != nil {
		return MatchView{}, nil, errors.New("shootout total is calculated")
	}
	if req.Place != nil {
		if *req.Place < 0 {
			return MatchView{}, nil, errors.New("bad place")
		}
		team.Place = *req.Place
	}

	if req.Theme != nil || req.Player != nil || req.Answer != nil || req.Mark != nil || req.Shootout != nil {
		isShootout := req.Shootout != nil && *req.Shootout
		themeCount := len(team.Themes)
		if isShootout {
			themeCount = len(team.ShootoutThemes)
		}
		if req.Theme == nil || *req.Theme < 0 || *req.Theme >= themeCount {
			return MatchView{}, nil, errors.New("bad theme index")
		}
		theme := &team.Themes[*req.Theme]
		if isShootout {
			theme = &team.ShootoutThemes[*req.Theme]
		}

		if req.Player != nil {
			player := strings.TrimSpace(*req.Player)
			if player != "" && !contains(team.Roster, player) {
				return MatchView{}, nil, errors.New("player is not in roster")
			}
			theme.Player = player
		}

		if req.Answer != nil || req.Mark != nil {
			if req.Answer == nil || *req.Answer < 0 || *req.Answer >= len(theme.Answers) {
				return MatchView{}, nil, errors.New("bad answer index")
			}
			if req.Mark == nil {
				return MatchView{}, nil, errors.New("missing mark")
			}
			theme.Answers[*req.Answer] = normalizeMark(*req.Mark)
		}
	}

	return s.commitLocked()
}

func (s *server) commitLocked() (MatchView, []byte, error) {
	normalizeState(&s.state)
	s.state.Revision++
	s.state.UpdatedAt = time.Now()
	if err := saveState(stateFile, s.state); err != nil {
		return MatchView{}, nil, err
	}

	view := buildView(s.state)
	data, err := json.Marshal(view)
	return view, data, err
}

func hasMatchEdit(req updateRequest) bool {
	return req.Action != "" ||
		req.Theme != nil ||
		req.Shootout != nil ||
		req.Answer != nil ||
		req.Mark != nil ||
		req.Player != nil ||
		req.Tiebreak != nil ||
		req.Place != nil
}

func hasTeamEdit(req updateRequest) bool {
	return req.Theme != nil ||
		req.Shootout != nil ||
		req.Answer != nil ||
		req.Mark != nil ||
		req.Player != nil ||
		req.Tiebreak != nil ||
		req.Place != nil
}

func buildView(state MatchState) MatchView {
	teams := make([]TeamView, len(state.Teams))
	for i, team := range state.Teams {
		teams[i] = scoreTeam(team)
	}

	standings := manualStandings(teams)
	for i := range standings {
		standing := standings[i]
		for teamIndex := range teams {
			if teams[teamIndex].Name == standing.Name {
				teams[teamIndex].Place = standing.Place
				break
			}
		}
	}

	return MatchView{
		Title:          state.Title,
		Finished:       state.Finished,
		Revision:       state.Revision,
		UpdatedAt:      state.UpdatedAt.Format(time.RFC3339),
		QuestionValues: questionValues,
		Teams:          teams,
		Standings:      standings,
	}
}

func scoreTeam(team TeamState) TeamView {
	view := TeamView{
		Name:           team.Name,
		Roster:         append([]string(nil), team.Roster...),
		Themes:         make([]ThemeView, len(team.Themes)),
		ShootoutThemes: make([]ThemeView, len(team.ShootoutThemes)),
		Place:          team.Place,
	}

	for i, theme := range team.Themes {
		tv := ThemeView{
			Player:  theme.Player,
			Answers: theme.Answers,
		}
		for answerIndex, mark := range theme.Answers {
			value := questionValues[answerIndex]
			switch normalizeMark(mark) {
			case "right":
				tv.Score += value
				view.Total += value
				view.Plus += value
				view.CorrectCounts[answerIndex]++
			case "wrong":
				tv.Score -= value
				view.Total -= value
				view.WrongCounts[answerIndex]++
			}
		}
		view.Themes[i] = tv
	}
	for i, theme := range team.ShootoutThemes {
		tv := scoreTheme(theme)
		view.ShootoutThemes[i] = tv
		view.ShootoutTotal += tv.Score
	}
	view.Tiebreak = view.ShootoutTotal
	return view
}

func assignComputedPlaces(state *MatchState) {
	type rankedTeam struct {
		index int
		view  TeamView
	}
	ranked := make([]rankedTeam, len(state.Teams))
	for index, team := range state.Teams {
		ranked[index] = rankedTeam{index: index, view: scoreTeam(team)}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return teamRanksHigher(ranked[i].view, ranked[j].view)
	})
	for place, team := range ranked {
		state.Teams[team.index].Place = float64(place + 1)
	}
}

func teamRanksHigher(a, b TeamView) bool {
	if a.Total != b.Total {
		return a.Total > b.Total
	}
	if a.ShootoutTotal != b.ShootoutTotal {
		return a.ShootoutTotal > b.ShootoutTotal
	}
	if a.Plus != b.Plus {
		return a.Plus > b.Plus
	}
	for i := len(a.CorrectCounts) - 1; i >= 0; i-- {
		if a.CorrectCounts[i] != b.CorrectCounts[i] {
			return a.CorrectCounts[i] > b.CorrectCounts[i]
		}
	}
	return false
}

func scoreTheme(theme ThemeEntry) ThemeView {
	view := ThemeView{
		Player:  theme.Player,
		Answers: theme.Answers,
	}
	for answerIndex, mark := range theme.Answers {
		value := questionValues[answerIndex]
		switch normalizeMark(mark) {
		case "right":
			view.Score += value
		case "wrong":
			view.Score -= value
		}
	}
	return view
}

func manualStandings(teams []TeamView) []StandingView {
	placed := make([]TeamView, 0, len(teams))
	unplaced := make([]TeamView, 0)
	for _, team := range teams {
		if team.Place > 0 {
			placed = append(placed, team)
		} else {
			unplaced = append(unplaced, team)
		}
	}
	for i := 1; i < len(placed); i++ {
		for j := i; j > 0 && placed[j-1].Place > placed[j].Place; j-- {
			placed[j-1], placed[j] = placed[j], placed[j-1]
		}
	}

	result := make([]StandingView, 0, len(teams))
	for _, team := range append(placed, unplaced...) {
		result = append(result, StandingView{
			Name:     team.Name,
			Place:    team.Place,
			Total:    team.Total,
			Plus:     team.Plus,
			Tiebreak: team.Tiebreak,
		})
	}
	return result
}

func normalizeMark(mark string) string {
	switch strings.ToLower(strings.TrimSpace(mark)) {
	case "right", "q", "й", "1", "+":
		return "right"
	case "wrong", "w", "ц", "-1", "-", "−1", "−":
		return "wrong"
	default:
		return ""
	}
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(data)
}

func writeSSE(w http.ResponseWriter, name string, revision int64, data []byte) {
	fmt.Fprintf(w, "event: %s\nid: %d\ndata: %s\n\n", name, revision, data)
}

func defaultMatch() MatchState {
	return MatchState{
		Title:     "Бой A",
		Revision:  1,
		UpdatedAt: time.Now(),
		Teams: []TeamState{
			{
				Name:   "ВШЭстером",
				Roster: []string{"Юлия Лапшина", "Савелий Кардашин", "Мария Крамкова", "Дамир Хамидуллин", "Андрей Акимов", "Максим Бобровицкий", "Захар Куренков"},
				Place:  3,
				Themes: []ThemeEntry{
					{Player: "Андрей Акимов", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Дамир Хамидуллин", Answers: [5]string{"", "", "", "right", ""}},
					{Player: "Юлия Лапшина", Answers: [5]string{"right", "right", "", "", "wrong"}},
					{Player: "Савелий Кардашин", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Андрей Акимов", Answers: [5]string{"wrong", "right", "", "", ""}},
					{Player: "Юлия Лапшина", Answers: [5]string{"right", "right", "", "", ""}},
					{Player: "Захар Куренков", Answers: [5]string{"", "", "right", "", ""}},
					{Player: "Дамир Хамидуллин", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Савелий Кардашин", Answers: [5]string{"", "right", "", "", ""}},
					{Player: "Дамир Хамидуллин", Answers: [5]string{"", "right", "", "", ""}},
					{Player: "Андрей Акимов", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Юлия Лапшина", Answers: [5]string{"wrong", "", "", "", ""}},
				},
			},
			{
				Name:   "Тина Терияки",
				Roster: []string{"Анна Гордеева", "Егор Абрамов", "Олег Шукаев", "Алексей Сазонов", "Кирилл Тищенко", "Андрей Кислуха"},
				Place:  2,
				Themes: []ThemeEntry{
					{Player: "Олег Шукаев", Answers: [5]string{"", "right", "", "right", ""}},
					{Player: "Алексей Сазонов", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Егор Абрамов", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Кирилл Тищенко", Answers: [5]string{"wrong", "", "", "", ""}},
					{Player: "Олег Шукаев", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Алексей Сазонов", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Кирилл Тищенко", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Егор Абрамов", Answers: [5]string{"", "", "", "right", ""}},
					{Player: "Кирилл Тищенко", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Алексей Сазонов", Answers: [5]string{"right", "", "right", "", ""}},
					{Player: "Олег Шукаев", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Егор Абрамов", Answers: [5]string{"", "", "", "", ""}},
				},
			},
			{
				Name:   "Вина России",
				Roster: []string{"Илья Пикалов", "Павел Соколов", "Дмитрий Федоров", "Никита Мирошин", "Евгения Королева", "Елена Трифонова", "Ольга Антропова"},
				Place:  4,
				Themes: []ThemeEntry{
					{Player: "Илья Пикалов", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Павел Соколов", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Евгения Королева", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Никита Мирошин", Answers: [5]string{"wrong", "", "", "", ""}},
					{Player: "Павел Соколов", Answers: [5]string{"right", "", "", "", ""}},
					{Player: "Никита Мирошин", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Илья Пикалов", Answers: [5]string{"", "right", "", "", ""}},
					{Player: "Дмитрий Федоров", Answers: [5]string{"", "", "", "wrong", ""}},
					{Player: "Павел Соколов", Answers: [5]string{"wrong", "", "", "", ""}},
					{Player: "Никита Мирошин", Answers: [5]string{"", "", "", "right", ""}},
					{Player: "Илья Пикалов", Answers: [5]string{"", "wrong", "", "", ""}},
					{Player: "Евгения Королева", Answers: [5]string{"right", "", "", "", ""}},
				},
			},
			{
				Name:   "Злая щитоспинка",
				Roster: []string{"Егор Дементьев", "Таисия Кирпикова", "Денис Красюк", "Михаил Московченко", "Амгалан Цыбенов", "Анна Рябикина"},
				Place:  1,
				Themes: []ThemeEntry{
					{Player: "Егор Дементьев", Answers: [5]string{"right", "", "right", "", ""}},
					{Player: "Амгалан Цыбенов", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Михаил Московченко", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Анна Рябикина", Answers: [5]string{"", "right", "", "", ""}},
					{Player: "Денис Красюк", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Амгалан Цыбенов", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Анна Рябикина", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Егор Дементьев", Answers: [5]string{"", "right", "right", "", ""}},
					{Player: "Денис Красюк", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Анна Рябикина", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Амгалан Цыбенов", Answers: [5]string{"", "", "", "", ""}},
					{Player: "Егор Дементьев", Answers: [5]string{"", "wrong", "", "right", ""}},
				},
			},
		},
	}
}
