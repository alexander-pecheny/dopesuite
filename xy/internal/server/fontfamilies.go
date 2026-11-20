package server

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// A Font Family (ADR-0025) is a board's uploaded font: one zip of TTF or OTF
// Faces. The server keeps two opaque things per Family, both encrypted under
// the board's key: the meta (its name, the zip's SHA-256 and its Faces) and
// the zip itself, as a blob. It sees how many there are and how big, never the
// hash, which would let it tell which known font a board uses.
//
// A Family is never changed in place: changing one is uploading a new zip
// under the same name, and the client deletes the old one once no Edited
// Image refers to it. So there is no patch and no replace.

// maxFontFamilyBytes caps one Family's zip: four Faces at ADR-0025's 20 MB a
// Face, with room for the zip's own framing.
const maxFontFamilyBytes = 84 << 20

type fontFamilyDTO struct {
	ID   int64  `json:"id"`
	Meta string `json:"meta"`
	Size int64  `json:"size"`
}

func scanFontFamilies(ctx context.Context, q querier, boardID int64) ([]fontFamilyDTO, error) {
	rows, err := q.QueryContext(ctx, `
select id, meta, size from font_families where board_id = ? and deleted_at is null order by id`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []fontFamilyDTO{}
	for rows.Next() {
		var f fontFamilyDTO
		var meta []byte
		if err := rows.Scan(&f.ID, &meta, &f.Size); err != nil {
			return nil, err
		}
		f.Meta = b64(meta)
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *server) handleListFontFamilies(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	fams, err := scanFontFamilies(r.Context(), s.db, bid)
	if handleErr(w, err) {
		return
	}
	writeJSON(w, fams)
}

// readFontUpload reads a multipart body of a "meta" field (base64 ciphertext)
// and a "blob" part (the encrypted zip), which it puts in the blob store. The
// caller owns the ref and removes it if its transaction fails.
func (s *server) readFontUpload(w http.ResponseWriter, r *http.Request) (meta []byte, ref string, size int64, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFontFamilyBytes+multipartMemory)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		httpError(w, http.StatusBadRequest, "bad multipart form")
		return nil, "", 0, false
	}
	meta, err := unb64(r.FormValue("meta"))
	if err != nil || len(meta) == 0 {
		httpError(w, http.StatusBadRequest, "invalid meta")
		return nil, "", 0, false
	}
	file, _, err := r.FormFile("blob")
	if err != nil {
		httpError(w, http.StatusBadRequest, "missing blob")
		return nil, "", 0, false
	}
	defer file.Close()
	ref, size, err = s.blobs.Put(io.LimitReader(file, maxFontFamilyBytes+1))
	if handleErr(w, err) {
		return nil, "", 0, false
	}
	if size > maxFontFamilyBytes {
		_ = s.blobs.Remove(ref)
		httpError(w, http.StatusRequestEntityTooLarge, "font family too large")
		return nil, "", 0, false
	}
	return meta, ref, size, true
}

func (s *server) handleCreateFontFamily(w http.ResponseWriter, r *http.Request) {
	_, bid, _, ok := s.requireBoard(w, r, "id")
	if !ok {
		return
	}
	meta, ref, size, ok := s.readFontUpload(w, r)
	if !ok {
		return
	}
	var id int64
	err := s.withWriteTx(r.Context(), "create-font-family", func(ctx context.Context, tx *sql.Tx) error {
		if err := s.enforceQuota(ctx, tx, bid, size+int64(len(meta))); err != nil {
			return err
		}
		now := rfc3339(time.Now())
		res, err := tx.ExecContext(ctx, `
insert into font_families(board_id, meta, size, blob_ref, created_at, updated_at) values(?, ?, ?, ?, ?, ?)`,
			bid, meta, size, ref, now, now)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		_ = s.blobs.Remove(ref)
		handleErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"id": id, "size": size})
}

func (s *server) handleGetFontFamily(w http.ResponseWriter, r *http.Request) {
	_, id, _, ok := s.requireChildAccess(w, r, childFontFamily)
	if !ok {
		return
	}
	var ref string
	if err := s.db.QueryRowContext(r.Context(), `select blob_ref from font_families where id = ?`, id).Scan(&ref); handleErr(w, err) {
		return
	}
	f, err := s.blobs.Open(ref)
	if err != nil {
		httpError(w, http.StatusNotFound, "blob missing")
		return
	}
	defer f.Close()
	// Ciphertext, like an attachment's: opaque to the browser until decrypted.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	if st, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", idstr.Format(st.Size()))
	}
	_, _ = io.Copy(w, f)
}

// handleDeleteFontFamily tombstones a Family. Whether an Edited Image still
// uses it is the client's to check: only the client can read the images.
func (s *server) handleDeleteFontFamily(w http.ResponseWriter, r *http.Request) {
	u, authed := s.requireUser(w, r)
	if !authed {
		return
	}
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	bid, err := childFontFamily.board(r.Context(), s.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		w.WriteHeader(http.StatusNoContent) // already gone
		return
	}
	if handleErr(w, err) {
		return
	}
	if _, err := boardRole(r.Context(), s.db, bid, u.UserID); handleErr(w, err) {
		return
	}
	err = s.withWriteTx(r.Context(), "delete-font-family", func(ctx context.Context, tx *sql.Tx) error {
		return tombstone(ctx, tx, "font_families", "id = ?", id)
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
