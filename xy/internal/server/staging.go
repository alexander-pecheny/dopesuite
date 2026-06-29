package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Handout image staging. When the user opens the "Генерация раздаток" screen the
// client decrypts the referenced images once and uploads them here; subsequent
// PDF / split_fit generations reuse the staged copies instead of re-decrypting +
// re-uploading them every time (which dominated the latency). A session stays
// alive while the tab heartbeats; the reaper deletes it once heartbeats stop for
// handoutSessionTTL (tab closed or backgrounded long enough). The client revives
// it by re-staging when a heartbeat 404s.
//
// Trade-off: the decrypted images sit in a temp dir for as long as the session
// lives — a deliberate, bounded extension of the brief plaintext exposure docx
// export already incurs (PLAN risk note). Files are wiped on close/expiry.

const (
	handoutSessionTTL   = 60 * time.Second
	handoutReapInterval = 20 * time.Second
	maxHandoutSessions  = 200
)

var errStagingFull = errors.New("too many handout sessions")

type handoutSession struct {
	userID   int64
	dir      string
	lastSeen time.Time
}

type handoutStaging struct {
	mu       sync.Mutex
	sessions map[string]*handoutSession
}

func newHandoutStaging() *handoutStaging {
	hs := &handoutStaging{sessions: map[string]*handoutSession{}}
	go hs.reapLoop()
	return hs
}

func randToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (hs *handoutStaging) reapLoop() {
	t := time.NewTicker(handoutReapInterval)
	defer t.Stop()
	for range t.C {
		hs.reap()
	}
}

func (hs *handoutStaging) reap() {
	now := time.Now()
	hs.mu.Lock()
	var stale []string
	for id, s := range hs.sessions {
		if now.Sub(s.lastSeen) > handoutSessionTTL {
			stale = append(stale, s.dir)
			delete(hs.sessions, id)
		}
	}
	hs.mu.Unlock()
	for _, dir := range stale {
		os.RemoveAll(dir)
	}
}

// create registers a fresh per-user session with its own temp dir.
func (hs *handoutStaging) create(userID int64) (string, *handoutSession, error) {
	id, err := randToken()
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "xy-handout-stage-*")
	if err != nil {
		return "", nil, err
	}
	s := &handoutSession{userID: userID, dir: dir, lastSeen: time.Now()}
	hs.mu.Lock()
	if len(hs.sessions) >= maxHandoutSessions {
		hs.mu.Unlock()
		os.RemoveAll(dir)
		return "", nil, errStagingFull
	}
	hs.sessions[id] = s
	hs.mu.Unlock()
	return id, s, nil
}

// touch returns the user's session, refreshing its TTL. ok=false if it's absent
// (reaped / never existed) or owned by another user.
func (hs *handoutStaging) touch(id string, userID int64) (*handoutSession, bool) {
	if id == "" {
		return nil, false
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()
	s := hs.sessions[id]
	if s == nil || s.userID != userID {
		return nil, false
	}
	s.lastSeen = time.Now()
	return s, true
}

// remove deletes the user's session and its files.
func (hs *handoutStaging) remove(id string, userID int64) {
	hs.mu.Lock()
	s := hs.sessions[id]
	if s != nil && s.userID == userID {
		delete(hs.sessions, id)
	} else {
		s = nil
	}
	hs.mu.Unlock()
	if s != nil {
		os.RemoveAll(s.dir)
	}
}

// ---- handlers ----

// handleHandoutsStage creates a session and writes the uploaded images into it,
// returning the session id. (Images are the multipart "img" parts.)
func (s *server) handleHandoutsStage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxExportRequest)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		httpError(w, http.StatusBadRequest, "bad multipart form")
		return
	}
	id, sess, err := s.staging.create(u.UserID)
	if err != nil {
		if errors.Is(err, errStagingFull) {
			httpError(w, http.StatusServiceUnavailable, "too many handout sessions, try again shortly")
			return
		}
		handleErr(w, err)
		return
	}
	if r.MultipartForm != nil {
		for _, fh := range r.MultipartForm.File["img"] {
			base := safeImageName(fh.Filename)
			if base == "" {
				continue
			}
			data, err := readUpload(fh)
			if err != nil {
				s.staging.remove(id, u.UserID)
				handleErr(w, err)
				return
			}
			if err := os.WriteFile(filepath.Join(sess.dir, base), data, 0o600); err != nil {
				s.staging.remove(id, u.UserID)
				handleErr(w, err)
				return
			}
		}
	}
	writeJSON(w, map[string]any{"session": id})
}

// handleHandoutsHeartbeat refreshes a session's TTL; 404 tells the client to
// re-stage (the session was reaped while the tab was backgrounded).
func (s *server) handleHandoutsHeartbeat(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if _, ok := s.staging.touch(r.FormValue("session"), u.UserID); !ok {
		httpError(w, http.StatusNotFound, "session expired")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleHandoutsUnstage drops a session (tab closed).
func (s *server) handleHandoutsUnstage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	s.staging.remove(r.URL.Query().Get("session"), u.UserID)
	w.WriteHeader(http.StatusNoContent)
}

// stagedImages returns the images staged under the request's "session" form
// field (refreshing the TTL), keyed by base name; nil when there's no live
// session. Merged into the render image set by the pdf / split_fit handlers.
func (s *server) stagedImages(r *http.Request, userID int64) map[string][]byte {
	sess, ok := s.staging.touch(r.FormValue("session"), userID)
	if !ok {
		return nil
	}
	entries, err := os.ReadDir(sess.dir)
	if err != nil {
		return nil
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if data, err := os.ReadFile(filepath.Join(sess.dir, e.Name())); err == nil {
			out[e.Name()] = data
		}
	}
	return out
}
