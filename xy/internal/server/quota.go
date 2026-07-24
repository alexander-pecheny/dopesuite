package server

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"

	"pecheny.me/dopecore/adminusers"
)

// humanMB renders a byte count as whole/one-decimal MiB for a user-facing string.
func humanMB(b int64) string {
	return strconv.FormatFloat(float64(b)/(1<<20), 'f', -1, 64) + " МБ"
}

// storageUsageSQL sums the bytes a user's own boards hold: attachment blobs plus
// every encrypted content column. Tombstones don't count, including everything
// under a tombstoned board or card (ADR-0002: quota-free until reaped). Shown on
// /profile and enforced on upload; the uid is bound five times, once per subquery.
const storageUsageSQL = `
select
  coalesce((select sum(a.size) from attachments a
            join boards b on b.id = a.board_id and b.deleted_at is null
            join cards ac on ac.id = a.card_id and ac.deleted_at is null
            where b.owner_user_id = ? and a.deleted_at is null), 0)
+ coalesce((select sum(length(c.description_enc) + coalesce(length(c.alias_enc), 0)) from cards c
            join boards b on b.id = c.board_id and b.deleted_at is null
            where b.owner_user_id = ? and c.deleted_at is null), 0)
+ coalesce((select sum(length(l.title_enc)) from lists l
            join boards b on b.id = l.board_id and b.deleted_at is null
            where b.owner_user_id = ? and l.deleted_at is null), 0)
+ coalesce((select sum(length(lb.name_enc) + length(lb.color_enc)) from labels lb
            join boards b on b.id = lb.board_id and b.deleted_at is null
            where b.owner_user_id = ? and lb.deleted_at is null), 0)
+ coalesce((select sum(length(t.payload_enc)) from timeline_events t
            join boards b on b.id = t.board_id and b.deleted_at is null
            join cards tc on tc.id = t.card_id and tc.deleted_at is null
            where b.owner_user_id = ? and t.deleted_at is null), 0)`

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func storageBytes(ctx context.Context, q rowQuerier, uid int64) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, storageUsageSQL, uid, uid, uid, uid, uid).Scan(&n)
	return n, err
}

// quotaExempt reports whether a user's storage is uncapped — currently just the
// admin account, whose own boards already dwarf any sane per-user cap.
func quotaExempt(username sql.NullString) bool {
	return username.Valid && username.String == adminusers.AdminUsername(adminUserEnv)
}

// isAdminUsername reports whether name is the configured admin login. Open
// telegram signup must never mint this account (admin rights follow the name),
// so it stays reservable only through adduser + the password-link path.
func isAdminUsername(name string) bool {
	return name == adminusers.AdminUsername(adminUserEnv)
}

// enforceQuota rejects a write of addBytes to a board that would push its OWNER
// over quota_bytes — attachments are attributed to whoever created the board, not
// the (possibly editor) uploader. Runs inside the upload transaction so the check
// sees committed state. The admin owner is exempt.
func enforceQuota(ctx context.Context, tx *sql.Tx, boardID, addBytes int64) error {
	var (
		ownerID  int64
		quota    int64
		username sql.NullString
	)
	if err := tx.QueryRowContext(ctx, `
select b.owner_user_id, u.quota_bytes, u.username
from boards b join users u on u.id = b.owner_user_id
where b.id = ?`, boardID).Scan(&ownerID, &quota, &username); err != nil {
		return err
	}
	if quotaExempt(username) {
		return nil
	}
	used, err := storageBytes(ctx, tx, ownerID)
	if err != nil {
		return err
	}
	if used+addBytes > quota {
		return errTooLarge("превышен лимит хранилища (" + humanMB(quota) + ")")
	}
	return nil
}

func (s *server) handleStorage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	used, err := storageBytes(r.Context(), s.db, u.UserID)
	if handleErr(w, err) {
		return
	}
	var quota int64
	if err := s.db.QueryRowContext(r.Context(), `select quota_bytes from users where id = ?`, u.UserID).Scan(&quota); handleErr(w, err) {
		return
	}
	resp := map[string]any{"used_bytes": used, "quota_bytes": quota}
	if quotaExempt(u.Username) {
		resp["unlimited"] = true
	}
	writeJSON(w, resp)
}
