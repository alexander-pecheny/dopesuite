package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	xystrings "xy/i18nstrings"

	"pecheny.me/dopecore/idstr"
)

// Attachments carry ciphertext bytes (already an xy envelope) plus encrypted
// metadata. The server stores them opaquely in the blob store and records mime/
// size in the clear (accepted metadata leakage — see the trust model in AGENTS.md).

const maxAttachmentBytes = 50 << 20 // 50 MiB ciphertext cap
// maxAttachmentRequest bounds the whole multipart upload (ciphertext + meta +
// boundary overhead) so a single request can't exhaust memory/temp disk. It
// sits comfortably above maxAttachmentBytes; over-cap requests fail the parse.
const maxAttachmentRequest = maxAttachmentBytes + 8<<20

// multipartMemory is how much of an upload form is held in memory before
// the rest spills to a temporary file.
const multipartMemory = 8 << 20

type attachmentDTO struct {
	ID          int64  `json:"id"`
	FilenameEnc string `json:"filename_enc"`
	Mime        string `json:"mime"`
	Size        int64  `json:"size"`
	Lossless    bool   `json:"lossless"`
	IsExcerpt   bool   `json:"is_excerpt"`
	CreatedAt   string `json:"created_at"`
	// Rev counts replacements (schema v14). Attachment bytes used to be immutable
	// for a given id, which both the client's object-URL memo and its IndexedDB
	// mirror rely on; a replace keeps the id, so the client keys those caches by
	// id+rev instead of re-checking every attachment it has ever downloaded.
	Rev int64 `json:"rev"`
}

func (s *server) handleListAttachments(w http.ResponseWriter, r *http.Request) {
	_, cardID, _, ok := s.requireChildAccess(w, r, childCard)
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
select id, filename_enc, mime, size, lossless, is_excerpt, rev, created_at
from attachments where card_id = ? and deleted_at is null order by id`, cardID)
	if handleErr(w, err) {
		return
	}
	defer rows.Close()
	out := []attachmentDTO{}
	for rows.Next() {
		var a attachmentDTO
		var fn []byte
		var lossless, excerpt int
		if err := rows.Scan(&a.ID, &fn, &a.Mime, &a.Size, &lossless, &excerpt, &a.Rev, &a.CreatedAt); handleErr(w, err) {
			return
		}
		a.FilenameEnc = b64(fn)
		a.Lossless = lossless != 0
		a.IsExcerpt = excerpt != 0
		out = append(out, a)
	}
	writeJSON(w, out)
}

// attachmentUpload is the "meta" JSON field of an attachment multipart body.
type attachmentUpload struct {
	FilenameEnc     string `json:"filename_enc"`
	Mime            string `json:"mime"`
	Lossless        bool   `json:"lossless"`
	IsExcerpt       bool   `json:"is_excerpt"`
	EventPayloadEnc string `json:"event_payload_enc"`
}

// readUpload parses the multipart body shared by create and replace: a "meta"
// JSON field plus a "blob" part holding the ciphertext bytes. On success the
// blob is already in the blob store; the caller owns that ref and must Remove
// it if the transaction referencing it fails.
func (s *server) readUpload(w http.ResponseWriter, r *http.Request) (meta attachmentUpload, filenameEnc []byte, ref string, size int64, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentRequest)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		httpError(w, http.StatusBadRequest, "bad multipart form")
		return
	}
	if err := json.Unmarshal([]byte(r.FormValue("meta")), &meta); err != nil {
		httpError(w, http.StatusBadRequest, "bad meta json")
		return
	}
	filenameEnc, err := unb64(meta.FilenameEnc)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid filename_enc")
		return
	}
	file, _, err := r.FormFile("blob")
	if err != nil {
		httpError(w, http.StatusBadRequest, "missing blob")
		return
	}
	defer file.Close()
	ref, size, err = s.blobs.Put(io.LimitReader(file, maxAttachmentBytes+1))
	if handleErr(w, err) {
		return meta, nil, "", 0, false
	}
	if size > maxAttachmentBytes {
		_ = s.blobs.Remove(ref)
		httpError(w, http.StatusRequestEntityTooLarge, "attachment too large")
		return meta, nil, "", 0, false
	}
	if meta.Mime == "" {
		meta.Mime = "application/octet-stream"
	}
	return meta, filenameEnc, ref, size, true
}

// attachmentRow is the plaintext side of a live attachment.
type attachmentRow struct {
	id, boardID, cardID, size int64
	ref                       string
}

// lookupAttachment reads the attachment named by the {id} path value and checks
// that the user is a member of its board. When there is no such attachment it
// calls onMissing to answer. ok=false means the response is already written.
func (s *server) lookupAttachment(w http.ResponseWriter, r *http.Request, onMissing func()) (uid int64, att attachmentRow, ok bool) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return 0, att, false
	}
	if att.id, ok = pathInt(w, r, "id"); !ok {
		return 0, att, false
	}
	err := s.db.QueryRowContext(r.Context(), `
select board_id, card_id, blob_ref, size from attachments where id = ? and deleted_at is null`, att.id).
		Scan(&att.boardID, &att.cardID, &att.ref, &att.size)
	if errors.Is(err, sql.ErrNoRows) {
		onMissing()
		return 0, att, false
	}
	if handleErr(w, err) {
		return 0, att, false
	}
	if _, err := boardRole(r.Context(), s.db, att.boardID, u.UserID); handleErr(w, err) {
		return 0, att, false
	}
	return u.UserID, att, true
}

// requireAttachment is lookupAttachment answering 404 for a missing attachment.
func (s *server) requireAttachment(w http.ResponseWriter, r *http.Request) (uid int64, att attachmentRow, ok bool) {
	return s.lookupAttachment(w, r, func() {
		httpError(w, http.StatusNotFound, xystrings.Default.Server.Attachment.NotFound())
	})
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// handleCreateAttachment accepts multipart/form-data: a "meta" JSON field
// (filename_enc, mime, lossless, is_excerpt, optional event_payload_enc) and a
// "blob" file part holding the ciphertext bytes.
func (s *server) handleCreateAttachment(w http.ResponseWriter, r *http.Request) {
	uid, cardID, bid, ok := s.requireChildAccess(w, r, childCard)
	if !ok {
		return
	}
	meta, filenameEnc, ref, size, ok := s.readUpload(w, r)
	if !ok {
		return
	}
	var id int64
	err := s.withWriteTx(r.Context(), "create-attachment", func(ctx context.Context, tx *sql.Tx) error {
		if err := enforceQuota(ctx, tx, bid, size); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
insert into attachments(board_id, card_id, filename_enc, mime, size, lossless, is_excerpt, blob_ref, created_at)
values(?, ?, ?, ?, ?, ?, ?, ?, ?)`, bid, cardID, filenameEnc, meta.Mime, size, boolInt(meta.Lossless), boolInt(meta.IsExcerpt), ref, rfc3339(time.Now()))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		if meta.EventPayloadEnc != "" {
			return appendEvent(ctx, tx, bid, cardID, "attach_add", uid, meta.EventPayloadEnc)
		}
		return nil
	})
	if err != nil {
		_ = s.blobs.Remove(ref)
		handleErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"id": id, "size": size})
}

// handleReplaceAttachment swaps an attachment's bytes in place, keeping its id
// (and so its excerpt flag and its place in the list) and bumping rev. Replacing
// rather than delete+upload is what lets a screenshot be re-shot without the
// card's other references to it going stale.
func (s *server) handleReplaceAttachment(w http.ResponseWriter, r *http.Request) {
	uid, old, ok := s.requireAttachment(w, r)
	if !ok {
		return
	}
	meta, filenameEnc, ref, size, ok := s.readUpload(w, r)
	if !ok {
		return
	}
	err := s.withWriteTx(r.Context(), "replace-attachment", func(ctx context.Context, tx *sql.Tx) error {
		if err := enforceQuota(ctx, tx, old.boardID, size-old.size); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
update attachments set filename_enc = ?, mime = ?, size = ?, lossless = ?, blob_ref = ?, rev = rev + 1
where id = ?`, filenameEnc, meta.Mime, size, boolInt(meta.Lossless), ref, old.id); err != nil {
			return err
		}
		if meta.EventPayloadEnc != "" {
			return appendEvent(ctx, tx, old.boardID, old.cardID, "attach_replace", uid, meta.EventPayloadEnc)
		}
		return nil
	})
	if err != nil {
		_ = s.blobs.Remove(ref)
		handleErr(w, err)
		return
	}
	_ = s.blobs.Remove(old.ref)
	writeJSON(w, map[string]any{"id": old.id, "size": size})
}

type patchAttachmentRequest struct {
	IsExcerpt *bool `json:"is_excerpt"`
}

// handlePatchAttachment flips an attachment's excerpt flag.
func (s *server) handlePatchAttachment(w http.ResponseWriter, r *http.Request) {
	_, att, ok := s.requireAttachment(w, r)
	if !ok {
		return
	}
	var req patchAttachmentRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.IsExcerpt == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	err := s.withWriteTx(r.Context(), "patch-attachment", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update attachments set is_excerpt = ? where id = ?`, boolInt(*req.IsExcerpt), att.id)
		return err
	})
	if handleErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	_, att, ok := s.requireAttachment(w, r)
	if !ok {
		return
	}
	f, err := s.blobs.Open(att.ref)
	if err != nil {
		httpError(w, http.StatusNotFound, "blob missing")
		return
	}
	defer f.Close()
	// Bytes are ciphertext; the client decrypts. Serve as opaque octet-stream.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	if st, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", idstr.Format(st.Size()))
	}
	_, _ = io.Copy(w, f)
}

func (s *server) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	// Deleting what is already gone is a success.
	uid, att, ok := s.lookupAttachment(w, r, func() { w.WriteHeader(http.StatusNoContent) })
	if !ok {
		return
	}
	eventEnc := r.URL.Query().Get("event_payload_enc")
	err := s.withWriteTx(r.Context(), "delete-attachment", func(ctx context.Context, tx *sql.Tx) error {
		if err := tombstone(ctx, tx, "attachments", "id = ?", att.id); err != nil {
			return err
		}
		if eventEnc != "" {
			return appendEvent(ctx, tx, att.boardID, att.cardID, "attach_remove", uid, eventEnc)
		}
		return nil
	})
	if handleErr(w, err) {
		return
	}
	// The blob stays on disk: a tombstoned attachment is restorable for 14 days,
	// and only the reaper destroys bytes (ADR-0002).
	w.WriteHeader(http.StatusNoContent)
}
