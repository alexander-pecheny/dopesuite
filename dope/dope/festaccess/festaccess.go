package festaccess

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"dope/dope/core"
	"dope/dope/festwrite"
	"dope/dope/roles"
	"dope/dope/store"
	"dope/dope/util"
)

type HostAccessMember struct {
	UserID    int64
	Nickname  string
	Role      string
	IsCreator bool
}

func MigrateFestOrganizerRoles(db *sql.DB) error {
	if err := store.AddColumnsIfMissing(db, "fest_organizers", []store.ColumnSpec{
		{Name: "role", Type: "TEXT NOT NULL DEFAULT 'admin' CHECK (role in ('creator','admin','host'))"},
	}); err != nil {
		return err
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
update fest_organizers
set role = 'admin'
where role is null or role not in ('creator', 'admin', 'host')`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
update fest_organizers
set role = 'admin'
where role = 'creator'
  and not exists (
    select 1 from fests f
    where f.id = fest_organizers.fest_id
      and f.created_by = fest_organizers.user_id
  )`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at)
select id, created_by, 'creator', created_at
from fests
where created_by is not null
on conflict(fest_id, user_id) do update set role = 'creator'`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
insert or ignore into schema_versions(version, applied_at)
values(11, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`); err != nil {
		return err
	}
	return tx.Commit()
}

func FestUserRoleFromQuery(ctx context.Context, q store.Queryer, festID, userID int64) (string, error) {
	var (
		createdBy sql.NullInt64
		role      sql.NullString
	)
	err := q.QueryRowContext(ctx, `
select f.created_by, o.role
from fests f
left join fest_organizers o on o.fest_id = f.id and o.user_id = ?
where f.id = ?`, userID, festID).Scan(&createdBy, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if createdBy.Valid && createdBy.Int64 == userID {
		return roles.Creator, nil
	}
	if !role.Valid {
		return "", nil
	}
	normalized := roles.Normalize(role.String)
	if normalized == roles.Creator {
		return roles.Admin, nil
	}
	return normalized, nil
}

func LoadFestAccessMembers(eng *core.Engine, ctx context.Context, festID int64) ([]HostAccessMember, error) {
	rows, err := eng.DB.QueryContext(ctx, `
select member.user_id,
       coalesce(nullif(u.username, ''), nullif(u.telegram_username, ''), 'user-' || u.id) as nickname,
       member.role
from (
  select f.created_by as user_id, 'creator' as role
  from fests f
  where f.id = ? and f.created_by is not null
  union all
  select o.user_id, case when o.role = 'creator' then 'admin' else coalesce(o.role, 'admin') end as role
  from fest_organizers o
  join fests f on f.id = o.fest_id
  where o.fest_id = ? and (f.created_by is null or o.user_id <> f.created_by)
) member
join users u on u.id = member.user_id
order by case member.role when 'creator' then 0 when 'admin' then 1 else 2 end,
         lower(nickname), member.user_id`, festID, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []HostAccessMember
	for rows.Next() {
		var member HostAccessMember
		if err := rows.Scan(&member.UserID, &member.Nickname, &member.Role); err != nil {
			return nil, err
		}
		member.Role = roles.Normalize(member.Role)
		member.IsCreator = member.Role == roles.Creator
		out = append(out, member)
	}
	return out, rows.Err()
}

func SaveFestAccess(eng *core.Engine, ctx context.Context, festID, actorID int64, form url.Values) error {
	tx, err := eng.BeginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	actorRole, err := FestUserRoleFromQuery(ctx, tx, festID, actorID)
	if err != nil {
		return err
	}
	if !roles.CanManageAccess(actorRole) {
		return errors.New("нет прав менять доступ")
	}

	creatorID, err := syncFestCreatorAccessTx(ctx, tx, festID)
	if err != nil {
		return err
	}

	current, err := loadFestAccessRoleMapTx(ctx, tx, festID, creatorID)
	if err != nil {
		return err
	}

	now := util.UtcNow()
	for userID, currentRole := range current {
		roleField := fmt.Sprintf("role_%d", userID)
		deleteField := fmt.Sprintf("delete_%d", userID)
		nextRole := roles.Normalize(form.Get(roleField))
		deleteMember := form.Get(deleteField) == "1"
		if err := applyFestAccessMemberTx(ctx, tx, festID, creatorID, userID, currentRole, nextRole, deleteMember, now); err != nil {
			return err
		}
	}

	addClicked := form.Get("add_access") == "1"
	nickname := strings.TrimSpace(form.Get("new_nickname"))
	if addClicked && nickname == "" {
		return errors.New("введите никнейм")
	}
	if addClicked {
		role := roles.Normalize(form.Get("new_role"))
		if !roles.Assignable(role) {
			return errors.New("для нового доступа выберите admin или host")
		}
		userID, err := lookupUserIDByNicknameTx(ctx, tx, nickname)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("пользователь %q не найден", nickname)
			}
			return err
		}
		if userID == creatorID {
			return errors.New("создатель уже есть в доступе")
		}
		if err := applyFestAccessMemberTx(ctx, tx, festID, creatorID, userID, current[userID], role, false, now); err != nil {
			return err
		}
	}

	if _, err := festwrite.BumpFestRevisionTx(ctx, tx, festID, "fest:access", "{}"); err != nil {
		return err
	}
	return tx.Commit()
}

func SaveFestAccessBulk(eng *core.Engine, ctx context.Context, festID, actorID int64, raw string) (int, error) {
	changes, err := roles.ParseBulkLines(raw)
	if err != nil {
		return 0, err
	}
	if len(changes) == 0 {
		return 0, errors.New("вставьте хотя бы одну строку")
	}

	tx, err := eng.BeginWriteTx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	actorRole, err := FestUserRoleFromQuery(ctx, tx, festID, actorID)
	if err != nil {
		return 0, err
	}
	if !roles.CanManageAccess(actorRole) {
		return 0, errors.New("нет прав менять доступ")
	}

	creatorID, err := syncFestCreatorAccessTx(ctx, tx, festID)
	if err != nil {
		return 0, err
	}
	current, err := loadFestAccessRoleMapTx(ctx, tx, festID, creatorID)
	if err != nil {
		return 0, err
	}

	now := util.UtcNow()
	for _, change := range changes {
		userID, err := lookupUserIDByNicknameTx(ctx, tx, change.Nickname)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return 0, fmt.Errorf("строка %d: пользователь %q не найден", change.Line, change.Nickname)
			}
			return 0, err
		}
		if err := applyFestAccessMemberTx(ctx, tx, festID, creatorID, userID, current[userID], change.Role, change.Delete, now); err != nil {
			return 0, fmt.Errorf("строка %d: %w", change.Line, err)
		}
		if change.Delete {
			delete(current, userID)
		} else {
			current[userID] = change.Role
		}
	}

	if _, err := festwrite.BumpFestRevisionTx(ctx, tx, festID, "fest:access", "{}"); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(changes), nil
}

func loadFestAccessRoleMapTx(ctx context.Context, tx *sql.Tx, festID, creatorID int64) (map[int64]string, error) {
	rows, err := tx.QueryContext(ctx, `
select o.user_id,
       case
         when ? > 0 and o.user_id = ? then 'creator'
         when o.role = 'creator' then 'admin'
         else coalesce(o.role, 'admin')
       end
from fest_organizers o
where o.fest_id = ?`, creatorID, creatorID, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	current := make(map[int64]string)
	for rows.Next() {
		var userID int64
		var role string
		if err := rows.Scan(&userID, &role); err != nil {
			return nil, err
		}
		current[userID] = roles.Normalize(role)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return current, nil
}

func applyFestAccessMemberTx(ctx context.Context, tx *sql.Tx, festID, creatorID, userID int64, currentRole, nextRole string, deleteMember bool, now string) error {
	if userID == creatorID || currentRole == roles.Creator {
		if deleteMember || (nextRole != "" && nextRole != roles.Creator) {
			return errors.New("создателя нельзя удалить или изменить")
		}
		_, err := tx.ExecContext(ctx, `
update fest_organizers set role = 'creator' where fest_id = ? and user_id = ?`, festID, userID)
		return err
	}
	if deleteMember {
		_, err := tx.ExecContext(ctx, `
delete from fest_organizers where fest_id = ? and user_id = ?`, festID, userID)
		return err
	}
	if nextRole == "" {
		nextRole = currentRole
	}
	if !roles.Assignable(nextRole) {
		return errors.New("роль должна быть admin или host")
	}
	_, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at)
values(?, ?, ?, ?)
on conflict(fest_id, user_id) do update set role = excluded.role`,
		festID, userID, nextRole, now)
	return err
}

func syncFestCreatorAccessTx(ctx context.Context, tx *sql.Tx, festID int64) (int64, error) {
	var (
		creatorID sql.NullInt64
		createdAt sql.NullString
	)
	err := tx.QueryRowContext(ctx, `
select created_by, created_at from fests where id = ?`, festID).Scan(&creatorID, &createdAt)
	if err != nil {
		return 0, err
	}
	if !creatorID.Valid {
		return 0, nil
	}
	addedAt := createdAt.String
	if strings.TrimSpace(addedAt) == "" {
		addedAt = util.UtcNow()
	}
	if _, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at)
values(?, ?, 'creator', ?)
on conflict(fest_id, user_id) do update set role = 'creator'`,
		festID, creatorID.Int64, addedAt); err != nil {
		return 0, err
	}
	return creatorID.Int64, nil
}

func lookupUserIDByNicknameTx(ctx context.Context, tx *sql.Tx, nickname string) (int64, error) {
	nickname = strings.TrimSpace(strings.TrimPrefix(nickname, "@"))
	var userID int64
	err := tx.QueryRowContext(ctx, `select id from users where username = ?`, nickname).Scan(&userID)
	if err == nil {
		return userID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	err = tx.QueryRowContext(ctx, `select id from users where telegram_username = ?`, nickname).Scan(&userID)
	return userID, err
}
