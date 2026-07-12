package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"
)

// Handout image staging. When the user opens the "Генерация раздаток" screen the
// client decrypts the referenced images once and uploads them here; subsequent
// PDF / split_fit generations reuse the staged copies instead of re-decrypting +
// re-uploading them every time (which dominated the latency). A session stays
// alive while the tab heartbeats; the reaper drops it once heartbeats stop for
// handoutSessionTTL (tab closed or backgrounded long enough). The client revives
// it by re-staging when a heartbeat 404s.
//
// The staged images are the user's decrypted handouts, so they are held in memory
// only — never written to the filesystem, where they would outlive a crash and sit
// there in plaintext. They are still a bounded extension of the brief plaintext
// exposure the docx export already incurs (PLAN risk note), and are dropped on
// close/expiry.

const (
	handoutSessionTTL   = 60 * time.Second
	handoutReapInterval = 20 * time.Second
	maxHandoutSessions  = 200
	// maxStagedBytes caps one session's images. Staging is in memory, so without
	// this a handful of sessions could pin an unbounded amount of it.
	maxStagedBytes = 128 << 20
)

var (
	errStagingFull  = errors.New("too many handout sessions")
	errStagedTooBig = errors.New("staged images too large")
)

type handoutSession struct {
	userID   int64
	images   map[string][]byte
	bytes    int64
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
	defer hs.mu.Unlock()
	for id, s := range hs.sessions {
		if now.Sub(s.lastSeen) > handoutSessionTTL {
			delete(hs.sessions, id) // the images go with it
		}
	}
}

// create registers a fresh per-user session holding the given images.
func (hs *handoutStaging) create(userID int64, images map[string][]byte) (string, error) {
	id, err := randToken()
	if err != nil {
		return "", err
	}
	var total int64
	for _, data := range images {
		total += int64(len(data))
	}
	if total > maxStagedBytes {
		return "", errStagedTooBig
	}
	s := &handoutSession{userID: userID, images: images, bytes: total, lastSeen: time.Now()}
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if len(hs.sessions) >= maxHandoutSessions {
		return "", errStagingFull
	}
	hs.sessions[id] = s
	return id, nil
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

// remove drops the user's session and its images.
func (hs *handoutStaging) remove(id string, userID int64) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if s := hs.sessions[id]; s != nil && s.userID == userID {
		delete(hs.sessions, id)
	}
}

// ---- handlers ----

// handleHandoutsStage creates a session holding the uploaded images (the
// multipart "img" parts) and returns its id.
func (s *server) handleHandoutsStage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxExportRequest)
	form, err := readMultipart(r, maxExportRequest)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	images := map[string][]byte{}
	for _, f := range form.Files("img") {
		if base := safeImageName(f.Filename); base != "" {
			images[base] = f.Data
		}
	}
	id, err := s.staging.create(u.UserID, images)
	if err != nil {
		switch {
		case errors.Is(err, errStagingFull):
			httpError(w, http.StatusServiceUnavailable, "too many handout sessions, try again shortly")
		case errors.Is(err, errStagedTooBig):
			httpError(w, http.StatusRequestEntityTooLarge, "staged images too large")
		default:
			handleErr(w, err)
		}
		return
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

// stagedImages returns the images staged under sessionID (refreshing the TTL),
// keyed by base name; nil when there's no live session. Merged into the render
// image set by the pdf / split_fit handlers.
func (s *server) stagedImages(sessionID string, userID int64) map[string][]byte {
	sess, ok := s.staging.touch(sessionID, userID)
	if !ok {
		return nil
	}
	out := make(map[string][]byte, len(sess.images))
	for name, data := range sess.images {
		out[name] = data
	}
	return out
}
