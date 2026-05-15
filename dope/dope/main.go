package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed static/*
var staticFiles embed.FS

const (
	stateFile                 = "match_state.json"
	themeCount                = 12
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
	tournamentID int64
	revision     int64
	data         []byte
}

type server struct {
	mu              sync.RWMutex
	db              *sql.DB
	tournamentID    int64
	activeGameID    int64
	activeMatchCode string
	state           MatchState
	subscribers     map[chan event]struct{}
	assets          fs.FS
	assetNoCache    bool
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
	mux.HandleFunc("/tournament/", srv.handleTournamentRouter)
	mux.HandleFunc("/register", srv.handleRegisterPage)
	mux.HandleFunc("/register/invite", srv.handleRegisterInviteSubmit)
	mux.HandleFunc("/register/username", srv.handleRegisterUsernameSubmit)
	mux.HandleFunc("/login", srv.serveStaticPage(assets, "static/login.html", noCacheAssets))
	mux.HandleFunc("/api/import", srv.handleImport)
	mux.HandleFunc("/host", srv.handleHostLanding)
	mux.HandleFunc("/host/", srv.handleHostRouter)
	mux.HandleFunc("/api/tournament/", srv.handleScopedAPI)
	mux.HandleFunc("/api/auth/register/start", srv.handleAuthRegisterStart)
	mux.HandleFunc("/api/auth/register/status", srv.handleAuthRegisterStatus)
	mux.HandleFunc("/api/auth/login", srv.handleAuthLogin)
	mux.HandleFunc("/api/auth/login-password", srv.handleAuthLoginPassword)
	mux.HandleFunc("/api/auth/logout", srv.handleAuthLogout)
	mux.HandleFunc("/api/auth/me", srv.handleAuthMe)
	mux.HandleFunc("/api/auth/username", srv.handleAuthUsername)
	mux.HandleFunc("/events", srv.handleEvents)
	mux.Handle("/static/", staticFileServer(assets, noCacheAssets))

	port := strings.TrimPrefix(os.Getenv("PORT"), ":")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
	log.Printf("serving static from %s", assetMode)
	log.Printf("listening on http://localhost%s/host and http://localhost%s/", addr, addr)
	log.Fatal(http.ListenAndServe(addr, mux))
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
	if !noCache {
		return handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		handler.ServeHTTP(w, r)
	})
}

func newServer() (*server, error) {
	dbPath := os.Getenv("DOPE_DB")
	if dbPath == "" {
		dbPath = dbFile
	}
	db, err := openTournamentDB(dbPath)
	if err != nil {
		return nil, err
	}
	if !isProdEnv() {
		ownerID, err := ensureDevUser(context.Background(), db)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		if err := ensureTestTournament(context.Background(), db, ownerID); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	tournamentID, gameID, matchCode, err := loadActiveContext(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if matchCode == "" {
		matchCode = defaultMatchCode
	}
	return &server{
		db:              db,
		tournamentID:    tournamentID,
		activeGameID:    gameID,
		activeMatchCode: matchCode,
		subscribers:     make(map[chan event]struct{}),
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
	tournamentID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("tournament_id")), 10, 64)
	if err != nil || tournamentID <= 0 {
		http.Error(w, "missing tournament_id", http.StatusBadRequest)
		return
	}
	if !s.authorizeTournamentRead(w, r, tournamentID) {
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
	s.addSubscriber(ch)
	defer s.removeSubscriber(ch)

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case ev := <-ch:
			if ev.tournamentID != tournamentID {
				continue
			}
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

func (s *server) addSubscriber(ch chan event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribers[ch] = struct{}{}
}

func (s *server) removeSubscriber(ch chan event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subscribers, ch)
	close(ch)
}

func (s *server) broadcast(ev event) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.subscribers {
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
		return s.applyMatchUpdate(s.tournamentID, s.activeMatchCode, req)
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
