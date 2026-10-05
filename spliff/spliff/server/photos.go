package spliffserver

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"time"

	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/idstr"

	"spliff/spliff/platform/imagex"
	"spliff/spliff/storage/store"
	"spliff/spliff/web/route"

	spliffstrings "spliff/i18nstrings"
)

const (
	// multipartOverhead is the room an upload's request gets beyond the photo
	// itself, for the multipart boundaries and headers.
	multipartOverhead = 1 << 16
	// multipartMemory is how much of a parsed form stays in memory.
	multipartMemory = 4 << 20
)

// A Photo is a picture a Member attaches to a Transaction for the others'
// reference. What is stored is never what the phone sent: the bytes are capped
// before they are decoded, decoded, scaled down and re-encoded as JPEG, so the
// EXIF — which carries where and when the receipt was photographed — does not
// travel with it.

func (s *server) handleUploadPhoto(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	t, err := store.TransactionByID(r.Context(), s.db, sc.TxID)
	if err != nil {
		return notFound(err)
	}
	encoded, err := readUpload(w, r)
	if err != nil {
		return err
	}
	id, err := s.storePhoto(r.Context(), sc, t, encoded)
	if err != nil {
		return err
	}
	return writeJSON(w, photoDTO{
		ID: id, URL: "/api/photos/" + idstr.Format(id),
		Width: encoded.Width, Height: encoded.Height,
	})
}

// storePhoto writes the blob, then the row and its History entry.
func (s *server) storePhoto(ctx context.Context, sc route.Scope, t store.Transaction, encoded imagex.Encoded) (int64, error) {
	ref, size, err := s.blobs.Put(newReader(encoded.Bytes))
	if err != nil {
		return 0, err
	}
	photo := store.Photo{Ref: ref, Width: encoded.Width, Height: encoded.Height, Bytes: size}
	now := rfc3339(time.Now())
	var id int64
	err = s.withWriteTx(ctx, "add-photo", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if id, err = store.InsertPhoto(ctx, tx, sc.TxID, sc.User.UserID, photo, now); err != nil {
			return err
		}
		return store.AppendHistory(ctx, tx, sc.TxID, sc.User.UserID,
			store.HistoryPhotoAdded, "", snapshotOf(withPhotoCount(t, len(t.Photos)+1)), now)
	})
	if err != nil {
		// The row did not land, so the bytes must not linger either.
		logDropped("add-photo: orphan blob", s.blobs.Remove(ref))
		return 0, err
	}
	return id, nil
}

// readUpload takes the photo out of a multipart request and re-encodes it.
func readUpload(w http.ResponseWriter, r *http.Request) (imagex.Encoded, error) {
	str := spliffstrings.Default
	// The cap is applied to the REQUEST, before anything is decoded: a decode
	// bomb is cheap to send and expensive to open.
	r.Body = http.MaxBytesReader(w, r.Body, imagex.MaxUploadBytes+multipartOverhead)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		return imagex.Encoded{}, corei18n.User(str.Transaction.Error.PhotoTooLarge())
	}
	file, _, err := r.FormFile("photo")
	if err != nil {
		return imagex.Encoded{}, corei18n.User(str.Transaction.Error.PhotoMissing())
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, imagex.MaxUploadBytes+1))
	if err != nil {
		return imagex.Encoded{}, err
	}
	if len(raw) > imagex.MaxUploadBytes {
		return imagex.Encoded{}, corei18n.User(str.Transaction.Error.PhotoTooLarge())
	}
	encoded, err := imagex.Reencode(raw)
	if err != nil {
		return imagex.Encoded{}, corei18n.User(str.Transaction.Error.PhotoNotAnImage())
	}
	return encoded, nil
}

// memberPhoto looks up the Photo the path names, for a Member of its Group
// only. Anybody else gets a 404, not a 403: the id is a guessable integer, and
// a 403 would say whether it names a real picture.
func (s *server) memberPhoto(r *http.Request, sc route.Scope) (int64, store.Photo, error) {
	notFoundErr := route.NotFound(spliffstrings.Default.Server.Error.NotFound())
	id, err := idstr.Parse(r.PathValue("id"))
	if err != nil {
		return 0, store.Photo{}, notFoundErr
	}
	photo, groupID, err := store.PhotoByID(r.Context(), s.db, id)
	if err != nil {
		return 0, store.Photo{}, notFound(err)
	}
	member, err := store.IsMember(r.Context(), s.db, groupID, sc.User.UserID)
	if err != nil {
		return 0, store.Photo{}, err
	}
	if !member {
		return 0, store.Photo{}, notFoundErr
	}
	return id, photo, nil
}

// photoTransaction is the Transaction a Photo is attached to.
func (s *server) photoTransaction(ctx context.Context, photoID int64) (store.Transaction, error) {
	var txID int64
	if err := s.db.QueryRowContext(ctx,
		`select transaction_id from transaction_photos where id = ?`, photoID).Scan(&txID); err != nil {
		return store.Transaction{}, notFound(err)
	}
	t, err := store.TransactionByID(ctx, s.db, txID)
	if err != nil {
		return store.Transaction{}, notFound(err)
	}
	return t, nil
}

func withPhotoCount(t store.Transaction, n int) store.Transaction {
	t.Photos = make([]store.Photo, n)
	return t
}

// handleGetPhoto serves the bytes to Members of the Group the Photo's
// Transaction belongs to, and to nobody else. The route cannot carry a {group},
// because a Photo's URL names the Photo — so the membership check is here.
func (s *server) handleGetPhoto(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	_, photo, err := s.memberPhoto(r, sc)
	if err != nil {
		return err
	}
	f, err := s.blobs.Open(photo.Ref)
	if err != nil {
		return notFound(err)
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", idstr.Format(photo.Bytes))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Private, because the picture is a Group's; immutable, because the blob
	// store is write-once and this id will never address other bytes.
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	if r.Method == http.MethodHead {
		return nil
	}
	_, err = io.Copy(w, f)
	return err
}

func (s *server) handleDeletePhoto(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	id, _, err := s.memberPhoto(r, sc)
	if err != nil {
		return err
	}
	t, err := s.photoTransaction(r.Context(), id)
	if err != nil {
		return err
	}
	txID := t.ID
	now := rfc3339(time.Now())
	var ref string
	err = s.withWriteTx(r.Context(), "delete-photo", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if ref, err = store.DeletePhoto(ctx, tx, id); err != nil {
			return err
		}
		return store.AppendHistory(ctx, tx, txID, sc.User.UserID,
			store.HistoryPhotoRemove, snapshotOf(t), snapshotOf(withPhotoCount(t, len(t.Photos)-1)), now)
	})
	if err != nil {
		return err
	}
	// After the commit: a blob removed inside the transaction would be gone even
	// if the transaction rolled back.
	logDropped("delete-photo: blob", s.blobs.Remove(ref))
	w.WriteHeader(http.StatusNoContent)
	return nil
}
