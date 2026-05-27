package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	festRoleCreator = "creator"
	festRoleAdmin   = "admin"
	festRoleHost    = "host"
)

type hostAccessMember struct {
	UserID    int64
	Nickname  string
	Role      string
	IsCreator bool
}

func migrateFestOrganizerRoles(db *sql.DB) error {
	if err := addColumnsIfMissing(db, "fest_organizers", []columnSpec{
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

func normalizeFestRole(role string) string {
	switch strings.TrimSpace(role) {
	case festRoleCreator:
		return festRoleCreator
	case festRoleAdmin:
		return festRoleAdmin
	case festRoleHost:
		return festRoleHost
	default:
		return ""
	}
}

func festRoleCanManageFest(role string) bool {
	role = normalizeFestRole(role)
	return role == festRoleCreator || role == festRoleAdmin
}

func festRoleCanManageAccess(role string) bool {
	return festRoleCanManageFest(role)
}

func festRoleCanDeleteFest(role string) bool {
	return normalizeFestRole(role) == festRoleCreator
}

func festRoleCanEditGameTables(role string) bool {
	return normalizeFestRole(role) != ""
}

func assignableFestRole(role string) bool {
	role = normalizeFestRole(role)
	return role == festRoleAdmin || role == festRoleHost
}

func (s *server) festUserRole(ctx context.Context, festID, userID int64) (string, error) {
	return festUserRoleFromQuery(ctx, s.db, festID, userID)
}

func festUserRoleFromQuery(ctx context.Context, q dbQueryer, festID, userID int64) (string, error) {
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
		return festRoleCreator, nil
	}
	if !role.Valid {
		return "", nil
	}
	normalized := normalizeFestRole(role.String)
	if normalized == festRoleCreator {
		return festRoleAdmin, nil
	}
	return normalized, nil
}

func (s *server) loadFestAccessMembers(ctx context.Context, festID int64) ([]hostAccessMember, error) {
	rows, err := s.db.QueryContext(ctx, `
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

	var out []hostAccessMember
	for rows.Next() {
		var member hostAccessMember
		if err := rows.Scan(&member.UserID, &member.Nickname, &member.Role); err != nil {
			return nil, err
		}
		member.Role = normalizeFestRole(member.Role)
		member.IsCreator = member.Role == festRoleCreator
		out = append(out, member)
	}
	return out, rows.Err()
}

func (s *server) saveFestAccess(ctx context.Context, festID, actorID int64, form url.Values) error {
	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	actorRole, err := festUserRoleFromQuery(ctx, tx, festID, actorID)
	if err != nil {
		return err
	}
	if !festRoleCanManageAccess(actorRole) {
		return errors.New("нет прав менять доступ")
	}

	creatorID, err := syncFestCreatorAccessTx(ctx, tx, festID)
	if err != nil {
		return err
	}

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
		return err
	}
	current := make(map[int64]string)
	for rows.Next() {
		var userID int64
		var role string
		if err := rows.Scan(&userID, &role); err != nil {
			rows.Close()
			return err
		}
		current[userID] = normalizeFestRole(role)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	now := utcNow()
	for userID, currentRole := range current {
		roleField := fmt.Sprintf("role_%d", userID)
		deleteField := fmt.Sprintf("delete_%d", userID)
		nextRole := normalizeFestRole(form.Get(roleField))
		deleteMember := form.Get(deleteField) == "1"
		if userID == creatorID || currentRole == festRoleCreator {
			if deleteMember || (nextRole != "" && nextRole != festRoleCreator) {
				return errors.New("создателя нельзя удалить или изменить")
			}
			if _, err := tx.ExecContext(ctx, `
update fest_organizers set role = 'creator' where fest_id = ? and user_id = ?`, festID, userID); err != nil {
				return err
			}
			continue
		}
		if deleteMember {
			if _, err := tx.ExecContext(ctx, `
delete from fest_organizers where fest_id = ? and user_id = ?`, festID, userID); err != nil {
				return err
			}
			continue
		}
		if nextRole == "" {
			nextRole = currentRole
		}
		if !assignableFestRole(nextRole) {
			return errors.New("роль должна быть admin или host")
		}
		if _, err := tx.ExecContext(ctx, `
update fest_organizers set role = ? where fest_id = ? and user_id = ?`,
			nextRole, festID, userID); err != nil {
			return err
		}
	}

	addClicked := form.Get("add_access") == "1"
	nickname := strings.TrimSpace(form.Get("new_nickname"))
	if addClicked && nickname == "" {
		return errors.New("введите никнейм")
	}
	if addClicked {
		role := normalizeFestRole(form.Get("new_role"))
		if !assignableFestRole(role) {
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
		if _, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at)
values(?, ?, ?, ?)
on conflict(fest_id, user_id) do update set role = excluded.role`,
			festID, userID, role, now); err != nil {
			return err
		}
	}

	if _, err := bumpFestRevisionTx(ctx, tx, festID, "fest:access", "{}"); err != nil {
		return err
	}
	return tx.Commit()
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
		addedAt = utcNow()
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
