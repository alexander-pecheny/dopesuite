package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Trello-compatible API surface for chgksuite (https://github.com/lemonsqueeze
// /chgksuite trello.py). chgksuite talks to a Trello board with exactly three
// calls, authenticated by `key` + `token` query/form params:
//
//   GET  /1/boards/{id}          → board with inline lists[], cards[], labels[]
//   GET  /1/boards/{id}/lists    → the board's lists (upload resolves list name→id)
//   POST /1/lists/{id}/cards     → create a card (name, desc)
//
// xy is end-to-end encrypted, so every text field (board/list/card/label name,
// card desc) is returned as the base64 ciphertext envelope exactly as the web
// client receives it — the consumer decrypts locally with the board passphrase
// (same envelope format crypto.js owns). The `key` param is the caller's Trello
// app key and is ignored; the `token` param is an xy API token (see tokens.go).
//
// board ids are xy's numeric ids (the path segment of /board/{id}); list/card
// ids are likewise numeric, rendered as strings to mirror Trello's opaque ids.

// ---- wire shapes (Trello JSON) ----

type trelloLabel struct {
	ID      string  `json:"id"`
	IDBoard string  `json:"idBoard"`
	Name    string  `json:"name"`
	Color   *string `json:"color"`
}

type trelloList struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Closed  bool   `json:"closed"`
	IDBoard string `json:"idBoard"`
	Pos     int    `json:"pos"`
}

type trelloCard struct {
	ID      string        `json:"id"`
	IDList  string        `json:"idList"`
	IDBoard string        `json:"idBoard"`
	Name    string        `json:"name"`
	Desc    string        `json:"desc"`
	Closed  bool          `json:"closed"`
	Pos     int           `json:"pos"`
	Labels  []trelloLabel `json:"labels"`
}

type trelloBoard struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Closed bool          `json:"closed"`
	URL    string        `json:"url"`
	Lists  []trelloList  `json:"lists"`
	Cards  []trelloCard  `json:"cards"`
	Labels []trelloLabel `json:"labels"`
	// Keymeta is an xy extension (not part of Trello): the passphrase-KDF
	// material a token holder needs to derive the board data key and decrypt
	// the ciphertext fields locally. Real Trello clients ignore unknown keys.
	Keymeta keymetaResponse `json:"keymeta"`
}

func idStr(id int64) string { return strconv.FormatInt(id, 10) }

// ---- token authentication ----

// authToken resolves the `token` param (query for GET, form body for POST) to a
// user id, rejecting missing/unknown/revoked/expired tokens with a Trello-style
// 401. It best-effort stamps last_used_at. The `key` param is ignored.
func (s *server) authToken(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(r.FormValue("token"))
	if raw == "" {
		httpError(w, http.StatusUnauthorized, "invalid token")
		return 0, false
	}
	var (
		id      int64
		uid     int64
		expires string
		revoked sql.NullString
	)
	err := s.db.QueryRowContext(r.Context(), `
select id, user_id, expires_at, revoked_at from api_tokens where token_hash = ?`,
		hashSessionToken(raw)).Scan(&id, &uid, &expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		httpError(w, http.StatusUnauthorized, "invalid token")
		return 0, false
	}
	if handleErr(w, err) {
		return 0, false
	}
	if revoked.Valid {
		httpError(w, http.StatusUnauthorized, "invalid token")
		return 0, false
	}
	if exp, _ := time.Parse(time.RFC3339, expires); time.Now().After(exp) {
		httpError(w, http.StatusUnauthorized, "expired token")
		return 0, false
	}
	_ = s.withWriteTx(r.Context(), "token-touch", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update api_tokens set last_used_at = ? where id = ?`, rfc3339(time.Now()), id)
		return err
	})
	return uid, true
}

// requireTokenBoard authenticates the token and verifies board access for the
// {id} path param, returning the board id.
func (s *server) requireTokenBoard(w http.ResponseWriter, r *http.Request) (uid, boardID int64, ok bool) {
	uid, ok = s.authToken(w, r)
	if !ok {
		return 0, 0, false
	}
	bid, okp := pathInt(w, r, "id")
	if !okp {
		return 0, 0, false
	}
	if _, err := boardRole(r.Context(), s.db, bid, uid); handleErr(w, err) {
		return 0, 0, false
	}
	return uid, bid, true
}

// ---- GET /1/boards/{id} ----

func (s *server) handleTrelloGetBoard(w http.ResponseWriter, r *http.Request) {
	_, bid, ok := s.requireTokenBoard(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	bidStr := idStr(bid)

	var (
		nameEnc       []byte
		salt, wrapped []byte
		verify        []byte
		kdfParams     string
	)
	if err := s.db.QueryRowContext(ctx,
		`select name_enc, kdf_salt, kdf_params, wrapped_key, verify_token from boards where id = ?`, bid).
		Scan(&nameEnc, &salt, &kdfParams, &wrapped, &verify); handleErr(w, err) {
		return
	}

	lists, err := scanLists(ctx, s.db, bid)
	if handleErr(w, err) {
		return
	}
	labels, err := scanLabels(ctx, s.db, bid)
	if handleErr(w, err) {
		return
	}
	cards, err := scanCards(ctx, s.db, bid)
	if handleErr(w, err) {
		return
	}

	labelByID := map[int64]trelloLabel{}
	outLabels := make([]trelloLabel, 0, len(labels))
	for _, l := range labels {
		tl := trelloLabel{ID: idStr(l.ID), IDBoard: bidStr, Name: l.NameEnc}
		labelByID[l.ID] = tl
		outLabels = append(outLabels, tl)
	}

	cardLabels, err := s.cardLabelMap(ctx, bid)
	if handleErr(w, err) {
		return
	}

	board := trelloBoard{
		ID:     bidStr,
		Name:   b64(nameEnc),
		URL:    trelloBoardURL(bid),
		Lists:  make([]trelloList, 0, len(lists)),
		Cards:  make([]trelloCard, 0, len(cards)),
		Labels: outLabels,
		Keymeta: keymetaResponse{
			KDFSalt: b64(salt), KDFParams: kdfParams,
			WrappedKey: b64(wrapped), VerifyToken: b64(verify),
		},
	}
	for i, l := range lists {
		board.Lists = append(board.Lists, trelloList{
			ID: idStr(l.ID), Name: l.TitleEnc, IDBoard: bidStr, Pos: (i + 1) * 1024,
		})
	}
	for i, c := range cards {
		tc := trelloCard{
			ID: idStr(c.ID), IDList: idStr(c.ListID), IDBoard: bidStr,
			Desc: c.DescEnc, Pos: (i + 1) * 1024, Labels: []trelloLabel{},
		}
		for _, lid := range cardLabels[c.ID] {
			if tl, ok := labelByID[lid]; ok {
				tc.Labels = append(tc.Labels, tl)
			}
		}
		board.Cards = append(board.Cards, tc)
	}
	writeJSON(w, board)
}

// cardLabelMap returns card_id → []label_id for a board's live cards.
func (s *server) cardLabelMap(ctx context.Context, boardID int64) (map[int64][]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
select cl.card_id, cl.label_id
from card_labels cl join cards c on c.id = cl.card_id
where c.board_id = ? and c.deleted_at is null`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]int64{}
	for rows.Next() {
		var cardID, labelID int64
		if err := rows.Scan(&cardID, &labelID); err != nil {
			return nil, err
		}
		out[cardID] = append(out[cardID], labelID)
	}
	return out, rows.Err()
}

func trelloBoardURL(bid int64) string {
	base := strings.TrimRight(os.Getenv("XY_PUBLIC_URL"), "/")
	if base == "" {
		base = "https://xy.pecheny.me"
	}
	return base + "/board/" + idStr(bid)
}

// ---- GET /1/boards/{id}/lists ----

func (s *server) handleTrelloGetLists(w http.ResponseWriter, r *http.Request) {
	_, bid, ok := s.requireTokenBoard(w, r)
	if !ok {
		return
	}
	lists, err := scanLists(r.Context(), s.db, bid)
	if handleErr(w, err) {
		return
	}
	bidStr := idStr(bid)
	out := make([]trelloList, 0, len(lists))
	for i, l := range lists {
		out = append(out, trelloList{ID: idStr(l.ID), Name: l.TitleEnc, IDBoard: bidStr, Pos: (i + 1) * 1024})
	}
	writeJSON(w, out)
}

// ---- POST /1/lists/{id}/cards ----

// handleTrelloCreateCard appends a card to a list. chgksuite posts form fields
// name + desc; xy has no separate card title (it's derived from the description
// client-side), so name is ignored. The board is end-to-end encrypted and the
// download path returns desc as the base64 ciphertext envelope, so this upload
// is symmetric: desc must be that same base64 envelope, which is decoded and
// stored into description_enc. The caller encrypts locally with the board key
// (the server never sees plaintext); a plaintext desc is rejected as non-base64.
func (s *server) handleTrelloCreateCard(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.authToken(w, r)
	if !ok {
		return
	}
	listID, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	bid, err := boardOfList(r.Context(), s.db, listID)
	if handleErr(w, err) {
		return
	}
	if _, err := boardRole(r.Context(), s.db, bid, uid); handleErr(w, err) {
		return
	}
	descEnc, err := unb64(strings.TrimSpace(r.FormValue("desc")))
	if err != nil || len(descEnc) == 0 {
		httpError(w, http.StatusBadRequest, "desc must be a base64 ciphertext envelope")
		return
	}

	now := time.Now()
	var id int64
	err = s.withWriteTx(r.Context(), "trello-create-card", func(ctx context.Context, tx *sql.Tx) error {
		var last sql.NullString
		if err := tx.QueryRowContext(ctx,
			`select max(rank) from cards where list_id = ? and deleted_at is null`, listID).Scan(&last); err != nil {
			return err
		}
		rank, err := rankAfter(last.String)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
insert into cards(board_id, list_id, kind, description_enc, rank, created_at, updated_at)
values(?, ?, 'normal', ?, ?, ?, ?)`, bid, listID, descEnc, rank, rfc3339(now), rfc3339(now))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if handleErr(w, err) {
		return
	}
	writeJSON(w, trelloCard{
		ID: idStr(id), IDList: idStr(listID), IDBoard: idStr(bid),
		Desc: b64(descEnc), Labels: []trelloLabel{},
	})
}
