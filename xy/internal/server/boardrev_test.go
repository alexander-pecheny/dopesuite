package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"
)

// revFixture is a board the randomized test writes to, and what it knows is on
// the board, so each step can pick something that exists.
type revFixture struct {
	t        *testing.T
	c        *apiClient
	board    string
	rnd      *rand.Rand
	lists    []int64 // normal lists
	appeals  int64   // the appeals list, whose cards take votes
	cards    []int64
	appealed []int64 // live appeal cards
	labels   []int64
	sessions []int64
	comments []int64
	n        int // a counter for unique text and ranks
}

func (f *revFixture) post(path string, body any) int64 {
	f.t.Helper()
	resp := f.c.do("POST", path, body)
	mustStatus(f.t, resp, 200)
	var out struct {
		ID int64 `json:"id"`
	}
	f.c.decode(resp, &out)
	return out.ID
}

func (f *revFixture) write(method, path string, body any, status int) {
	f.t.Helper()
	mustStatus(f.t, f.c.do(method, path, body), status)
}

func (f *revFixture) text() string { f.n++; return "текст " + strconv.Itoa(f.n) }
func (f *revFixture) rank() string { f.n++; return fmt.Sprintf("r%06d", f.rnd.Intn(1000000)) }

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.Intn(len(xs))] }

func without(xs []int64, x int64) []int64 {
	return slices.DeleteFunc(slices.Clone(xs), func(y int64) bool { return y == x })
}

// subset picks a random subset of xs.
func subset(r *rand.Rand, xs []int64) []int64 {
	out := []int64{}
	for _, x := range xs {
		if r.Intn(2) == 0 {
			out = append(out, x)
		}
	}
	return out
}

func (f *revFixture) createCard() {
	list := pick(f.rnd, f.lists)
	id := f.post("/api/lists/"+itoa(list)+"/cards", map[string]string{"description": enc(f.text()), "rank": f.rank(), "kind": "question"})
	f.cards = append(f.cards, id)
}

func (f *revFixture) createAppeal() {
	id := f.post("/api/lists/"+itoa(f.appeals)+"/cards", map[string]string{"description": enc("{}"), "rank": f.rank(), "kind": "appeal"})
	f.appealed = append(f.appealed, id)
}

// step makes one random write. Every kind of write that reaches the snapshot
// or the comments is here, including the ones that change nothing a delta
// sends, such as a list rename.
func (f *revFixture) step() string {
	r := f.rnd
	if len(f.cards) == 0 {
		f.createCard()
		return "create card"
	}
	card := pick(r, f.cards)
	cardPath := "/api/cards/" + itoa(card)
	switch r.Intn(18) {
	case 0, 1:
		f.createCard()
		return "create card"
	case 2:
		f.write("PATCH", cardPath, map[string]string{"description": enc(f.text())}, 204)
		return "edit description"
	case 3:
		alias := ""
		if r.Intn(2) == 0 {
			alias = enc(f.text())
		}
		f.write("PATCH", cardPath, map[string]string{"alias": alias}, 204)
		return "alias"
	case 4:
		f.write("PATCH", cardPath, map[string]string{"rank": f.rank()}, 204)
		return "reorder card"
	case 5:
		f.write("PATCH", cardPath, map[string]any{"list_id": pick(r, f.lists), "rank": f.rank()}, 204)
		return "move card"
	case 6:
		f.write("DELETE", cardPath, nil, 204)
		f.cards = without(f.cards, card)
		return "delete card"
	case 7:
		// A label with a session needs the card to have been played there.
		played := f.playings(card)
		labels := []map[string]any{}
		for _, l := range subset(r, f.labels) {
			a := map[string]any{"label_id": l}
			if len(played) > 0 && r.Intn(2) == 0 {
				a["session_id"] = pick(r, played)
			}
			labels = append(labels, a)
		}
		f.write("PUT", cardPath+"/labels", map[string]any{"labels": labels}, 204)
		return "set labels"
	case 8:
		f.write("PUT", cardPath+"/sessions", map[string]any{"session_ids": subset(r, f.sessions)}, 204)
		return "set playings"
	case 9:
		if len(f.appealed) == 0 || r.Intn(3) == 0 {
			f.createAppeal()
			return "create appeal"
		}
		appeal := "/api/cards/" + itoa(pick(r, f.appealed)) + "/vote"
		if r.Intn(3) == 0 {
			f.write("DELETE", appeal, nil, 204)
			return "delete vote"
		}
		f.write("PUT", appeal, map[string]string{"value": enc(f.text())}, 204)
		return "vote"
	case 10:
		if len(f.lists) > 1 && r.Intn(2) == 0 {
			list := pick(r, f.lists)
			f.write("DELETE", "/api/lists/"+itoa(list), nil, 204)
			f.lists = without(f.lists, list)
			f.cards = f.liveCards()
			return "delete list"
		}
		f.lists = append(f.lists, f.post("/api/boards/"+f.board+"/lists", map[string]string{"title": enc(f.text()), "rank": f.rank()}))
		return "create list"
	case 11:
		f.write("PATCH", "/api/lists/"+itoa(pick(r, f.lists)), map[string]string{"title": enc(f.text())}, 204)
		return "rename list"
	case 12:
		if len(f.labels) > 0 && r.Intn(3) == 0 {
			label := pick(r, f.labels)
			f.write("DELETE", "/api/labels/"+itoa(label), nil, 204)
			f.labels = without(f.labels, label)
			return "delete label"
		}
		f.labels = append(f.labels, f.post("/api/boards/"+f.board+"/labels", map[string]string{"name": enc(f.text()), "color": enc("#fff")}))
		return "create label"
	case 13:
		if len(f.sessions) > 0 && r.Intn(3) == 0 {
			session := pick(r, f.sessions)
			f.write("DELETE", "/api/sessions/"+itoa(session), nil, 204)
			f.sessions = without(f.sessions, session)
			return "delete session"
		}
		f.sessions = append(f.sessions, f.post("/api/boards/"+f.board+"/sessions", map[string]string{"meta": enc(f.text())}))
		return "create session"
	case 14:
		f.write("PATCH", cardPath, map[string]string{"kind": pick(r, []string{"question", "heading", "other"})}, 204)
		return "change kind"
	default:
		return f.commentStep(cardPath)
	}
}

func (f *revFixture) commentStep(cardPath string) string {
	r := f.rnd
	if len(f.comments) > 0 && r.Intn(2) == 0 {
		comment := pick(r, f.comments)
		if r.Intn(2) == 0 {
			f.write("DELETE", "/api/comments/"+itoa(comment), nil, 204)
			f.comments = without(f.comments, comment)
			return "delete comment"
		}
		f.write("PATCH", "/api/comments/"+itoa(comment), map[string]string{"payload": enc(f.text())}, 204)
		return "edit comment"
	}
	f.write("POST", cardPath+"/comments", map[string]string{"payload": enc(f.text())}, 204)
	f.comments = f.liveComments()
	return "add comment"
}

func (f *revFixture) playings(card int64) []int64 {
	out := []int64{}
	for _, p := range getSnapshotFor(f.t, f.c, f.board).CardSessions {
		if p.CardID == card {
			out = append(out, p.SessionID)
		}
	}
	return out
}

func (f *revFixture) liveCards() []int64 {
	snap := getSnapshotFor(f.t, f.c, f.board)
	out := []int64{}
	for _, c := range snap.Cards {
		if c.Kind != "appeal" {
			out = append(out, c.ID)
		}
	}
	return out
}

func (f *revFixture) liveComments() []int64 {
	out := []int64{}
	for _, c := range f.commentsFull() {
		out = append(out, c.ID)
	}
	return out
}

// fullComments is every live comment, the old way.
func (f *revFixture) commentsFull() []boardCommentDTO {
	resp := f.c.do("GET", "/api/boards/"+f.board+"/comments", nil)
	mustStatus(f.t, resp, 200)
	var out []boardCommentDTO
	f.c.decode(resp, &out)
	return out
}

func (f *revFixture) snapshotSince(rev int64) boardSnapshot {
	f.t.Helper()
	resp := f.c.do("GET", "/api/boards/"+f.board+"?since="+strconv.FormatInt(rev, 10), nil)
	mustStatus(f.t, resp, 200)
	var snap boardSnapshot
	f.c.decode(resp, &snap)
	return snap
}

func (f *revFixture) commentsSince(rev int64) boardCommentsDelta {
	f.t.Helper()
	resp := f.c.do("GET", "/api/boards/"+f.board+"/comments?since="+strconv.FormatInt(rev, 10), nil)
	mustStatus(f.t, resp, 200)
	var d boardCommentsDelta
	f.c.decode(resp, &d)
	return d
}

// mergeSnapshot applies a delta to the snapshot it was asked against, the way
// the client does (web/ts/snapdelta.ts).
func mergeSnapshot(t *testing.T, base, d boardSnapshot) boardSnapshot {
	t.Helper()
	if d.Since == nil {
		return d
	}
	if *d.Since != base.Rev {
		t.Fatalf("delta since %d applied to rev %d", *d.Since, base.Rev)
	}
	changed := map[int64]bool{}
	for _, id := range d.GoneCards {
		changed[id] = true
	}
	for _, c := range d.Cards {
		changed[c.ID] = true
	}
	had := map[int64]cardDTO{}
	for _, c := range base.Cards {
		had[c.ID] = c
	}
	out := d
	out.Since, out.GoneCards = nil, nil
	out.Cards = nil
	for _, c := range base.Cards {
		if !changed[c.ID] {
			out.Cards = append(out.Cards, c)
		}
	}
	for _, c := range d.Cards {
		if c.Same {
			prev, ok := had[c.ID]
			if !ok {
				t.Fatalf("delta keeps card %d, which the base does not have", c.ID)
			}
			prev.ListID, prev.Rank = c.ListID, c.Rank
			c = prev
		}
		out.Cards = append(out.Cards, c)
	}
	out.CardLabels = append(keep(base.CardLabels, changed, func(x cardLabelDTO) int64 { return x.CardID }), d.CardLabels...)
	out.CardSessions = append(keep(base.CardSessions, changed, func(x cardSessionDTO) int64 { return x.CardID }), d.CardSessions...)
	out.Votes = append(keep(base.Votes, changed, func(x voteDTO) int64 { return x.CardID }), d.Votes...)
	return out
}

func keep[T any](xs []T, drop map[int64]bool, card func(T) int64) []T {
	out := []T{}
	for _, x := range xs {
		if !drop[card(x)] {
			out = append(out, x)
		}
	}
	return out
}

// canonical sorts the parts of a snapshot whose order a merge does not keep,
// and renders it, so two snapshots compare as text.
func canonical(t *testing.T, s boardSnapshot) string {
	t.Helper()
	slices.SortFunc(s.Cards, func(a, b cardDTO) int { return int(a.ID - b.ID) })
	key := func(parts ...any) string { return fmt.Sprint(parts...) }
	slices.SortFunc(s.CardLabels, func(a, b cardLabelDTO) int {
		return compareStrings(key(a.CardID, " ", a.LabelID, " ", deref(a.SessionID)), key(b.CardID, " ", b.LabelID, " ", deref(b.SessionID)))
	})
	slices.SortFunc(s.CardSessions, func(a, b cardSessionDTO) int {
		return compareStrings(key(a.CardID, " ", a.SessionID), key(b.CardID, " ", b.SessionID))
	})
	slices.SortFunc(s.Votes, func(a, b voteDTO) int {
		return compareStrings(key(a.CardID, " ", a.UserID), key(b.CardID, " ", b.UserID))
	})
	for _, xs := range []any{&s.Cards, &s.CardLabels, &s.CardSessions, &s.Votes} {
		v := reflect.ValueOf(xs).Elem()
		if v.Len() == 0 {
			v.Set(reflect.MakeSlice(v.Type(), 0, 0))
		}
	}
	b, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// revStepsPerSeed is how many random writes each seed makes. Each step checks
// a delta against a fresh full read, from a random earlier point.
const revStepsPerSeed = 120

// TestBoardDeltaMatchesFullSnapshot is the guard on ADR-0023. It makes random
// writes of every kind, and after each one takes a delta from a random earlier
// snapshot, merges it the way the client does, and checks that the result is
// exactly the full snapshot. A write path the triggers miss fails here.
func TestBoardDeltaMatchesFullSnapshot(t *testing.T) {
	for seed := int64(1); seed <= 3; seed++ {
		t.Run("seed"+strconv.FormatInt(seed, 10), func(t *testing.T) {
			f := newRevFixture(t, seed)
			type point struct {
				snap     boardSnapshot
				comments map[int64]boardCommentDTO
				rev      int64
			}
			first := getSnapshotFor(t, f.c, f.board)
			history := []point{{snap: first, comments: commentMap(f.commentsFull()), rev: first.Rev}}
			moved := 0 // cards a delta sent without their content
			for i := 0; i < revStepsPerSeed; i++ {
				what := f.step()
				full := getSnapshotFor(t, f.c, f.board)
				base := pick(f.rnd, history)
				d := f.snapshotSince(base.rev)
				if d.Since == nil {
					t.Fatalf("step %d (%s): no delta from rev %d", i, what, base.rev)
				}
				for _, c := range d.Cards {
					if c.Same {
						moved++
					}
				}
				if got, want := canonical(t, mergeSnapshot(t, base.snap, d)), canonical(t, full); got != want {
					t.Fatalf("step %d (%s): delta from rev %d does not match the full snapshot\nmerged: %s\nfull:   %s", i, what, base.rev, got, want)
				}
				comments := mergeComments(t, base.comments, f.commentsSince(base.rev), full.Cards)
				if want := commentMap(f.commentsFull()); !reflect.DeepEqual(comments, want) {
					t.Fatalf("step %d (%s): comments delta from rev %d = %v, want %v", i, what, base.rev, comments, want)
				}
				history = append(history, point{snap: full, comments: comments, rev: full.Rev})
			}
			if moved == 0 {
				t.Fatal("no delta sent a card without its content, so that path went untested")
			}
		})
	}
}

func commentMap(xs []boardCommentDTO) map[int64]boardCommentDTO {
	out := map[int64]boardCommentDTO{}
	for _, x := range xs {
		out[x.ID] = x
	}
	return out
}

// mergeComments applies a comments delta the way the search index does. A
// comment on a card that is gone is dropped too, as putCards drops it.
func mergeComments(t *testing.T, base map[int64]boardCommentDTO, d boardCommentsDelta, live []cardDTO) map[int64]boardCommentDTO {
	t.Helper()
	if d.Full {
		t.Fatalf("comments delta is full")
	}
	onLive := map[int64]bool{}
	for _, c := range live {
		onLive[c.ID] = true
	}
	out := map[int64]boardCommentDTO{}
	for id, c := range base {
		if onLive[c.CardID] {
			out[id] = c
		}
	}
	for _, id := range d.Gone {
		delete(out, id)
	}
	for _, c := range d.Comments {
		out[c.ID] = c
	}
	return out
}

func newRevFixture(t *testing.T, seed int64) *revFixture {
	c, boardID, listID := boardWithList(t)
	f := &revFixture{t: t, c: c, board: boardID, rnd: rand.New(rand.NewSource(seed))}
	f.lists = []int64{mustAtoi(t, listID)}
	f.appeals = f.post("/api/boards/"+boardID+"/lists", map[string]string{"title": enc("Апелляции"), "rank": "z", "type": "appeals", "meta": enc("{}")})
	return f
}

// TestBoardDeltaFallsBackToFull checks the cases a delta cannot serve: no
// since, junk, a since from the future, and one older than a reaped deletion.
func TestBoardDeltaFallsBackToFull(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 770150, "revs")
	resp := c.do("POST", "/api/boards", map[string]string{
		"name": "b", "kdf_salt": enc("s"),
		"kdf_params": `{"kdf":"scrypt","N":1,"r":1,"p":1}`, "wrapped_key": enc("w"), "verify_token": enc("v"),
	})
	mustStatus(t, resp, 200)
	var b struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &b)
	boardID := itoa(b.ID)
	f := &revFixture{t: t, c: c, board: boardID, rnd: rand.New(rand.NewSource(1))}
	f.lists = []int64{f.post("/api/boards/"+boardID+"/lists", map[string]string{"title": enc("l"), "rank": "m"})}
	f.createCard()
	f.createCard()
	start := getSnapshotFor(t, c, boardID).Rev

	if d := f.snapshotSince(start); d.Since == nil || len(d.Cards) != 0 {
		t.Fatalf("delta with nothing changed = %+v", d)
	}
	for _, bad := range []string{"junk", strconv.FormatInt(start+1, 10)} {
		resp := c.do("GET", "/api/boards/"+boardID+"?since="+bad, nil)
		mustStatus(t, resp, 200)
		var snap boardSnapshot
		c.decode(resp, &snap)
		if snap.Since != nil || len(snap.Cards) != 2 {
			t.Fatalf("since=%s gave %+v, want the full snapshot", bad, snap)
		}
	}

	f.write("DELETE", "/api/cards/"+itoa(f.cards[0]), nil, 204)
	if d := f.snapshotSince(start); d.Since == nil || !slices.Equal(d.GoneCards, []int64{f.cards[0]}) {
		t.Fatalf("delta after a delete = %+v, want the card gone", d)
	}
	// Once the tombstone is reaped, a delta from before it could not tell the
	// client the card is gone, so it gets the full snapshot.
	if _, err := srv.reapOnce(t.Context(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if d := f.snapshotSince(start); d.Since != nil {
		t.Fatalf("delta from before a reaped deletion = %+v, want full", d)
	}
	if d := f.commentsSince(start); !d.Full {
		t.Fatalf("comments delta from before a reaped deletion = %+v, want full", d)
	}
}

// TestRevTriggersExist fails when a migration that rebuilds a table drops the
// triggers that number its changes (see v33 in db.go).
func TestRevTriggersExist(t *testing.T) {
	_, srv := newTestServer(t)
	want := []string{
		"card_labels_rev_delete", "card_labels_rev_insert", "card_labels_rev_update",
		"card_sessions_rev_delete", "card_sessions_rev_insert", "card_sessions_rev_update",
		"card_votes_rev_delete", "card_votes_rev_insert", "card_votes_rev_update",
		"cards_rev_content", "cards_rev_insert", "cards_rev_position",
		"comments_rev_insert", "comments_rev_update",
	}
	got := triggerNames(t, srv.db)
	if !slices.Equal(got, want) {
		t.Fatalf("triggers = %v, want %v", got, want)
	}
}

func triggerNames(t *testing.T, db *sql.DB) []string {
	rows, err := db.Query(`select name from sqlite_master where type = 'trigger' order by name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	return out
}
