package festaccess

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/platform/roles"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	"pecheny.me/dopecore/adminusers"
	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/idstr"
)

// SiteAdminEnv names the one account that runs /admin (default "pecheny").
const SiteAdminEnv = "DOPE_ADMIN_USER"

// IsSiteAdmin says whether username is the site admin's.
func IsSiteAdmin(username string) bool {
	return username != "" && username == adminusers.AdminUsername(SiteAdminEnv)
}

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
	for _, stmt := range organizerRoleMigration {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// organizerRoleMigration runs in order: unknown roles become admin, a
// "creator" who did not create the fest becomes admin, every fest's creator
// gets the creator row, and the migration records itself.
var organizerRoleMigration = []string{`
update fest_organizers
set role = 'admin'
where role is null or role not in ('creator', 'admin', 'host')`, `
update fest_organizers
set role = 'admin'
where role = 'creator'
  and not exists (
    select 1 from fests f
    where f.id = fest_organizers.fest_id
      and f.created_by = fest_organizers.user_id
  )`, `
insert into fest_organizers(fest_id, user_id, role, added_at)
select id, created_by, 'creator', created_at
from fests
where created_by is not null
on conflict(fest_id, user_id) do update set role = 'creator'`, `
insert or ignore into schema_versions(version, applied_at)
values(11, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`,
}

func FestUserRoleFromQuery(ctx context.Context, q store.Queryer, festID, userID int64) (string, error) {
	var (
		createdBy sql.NullInt64
		role      sql.NullString
		username  sql.NullString
	)
	err := q.QueryRowContext(ctx, festUserRoleQuery, userID, userID, festID).Scan(&createdBy, &role, &username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if createdBy.Valid && createdBy.Int64 == userID {
		return roles.Creator, nil
	}
	return organizerRole(role, username), nil
}

const festUserRoleQuery = `
select f.created_by, o.role, (select u.username from users u where u.id = ?)
from fests f
left join fest_organizers o on o.fest_id = f.id and o.user_id = ?
where f.id = ?`

// organizerRole is the role a user who did not create the fest holds on it:
// their organizer row's, with a stale "creator" read as admin.
func organizerRole(role, username sql.NullString) string {
	if !role.Valid {
		// The site admin helps on every fest without being added to it, with
		// the rights of a fest admin: everything except deleting the fest.
		if username.Valid && IsSiteAdmin(username.String) {
			return roles.Admin
		}
		return ""
	}
	normalized := roles.Normalize(role.String)
	if normalized == roles.Creator {
		return roles.Admin
	}
	return normalized
}

func LoadFestAccessMembers(eng *core.Engine, ctx context.Context, festID int64) ([]HostAccessMember, error) {
	rows, err := eng.DB.QueryContext(ctx, festAccessMembersQuery, festID, festID)
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

// festAccessMembersQuery lists a fest's creator and organizers, creator
// first, then admins, then hosts.
const festAccessMembersQuery = `
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
         lower(nickname), member.user_id`

// SaveFestAccessTx applies the dashboard's access form: each member's role or
// removal, the Games each host may run, and a member added by nickname. The
// caller records the revision (fest:access).
func SaveFestAccessTx(ctx context.Context, tx *sql.Tx, festID, actorID int64, form url.Values) error {
	creatorID, current, err := accessEditTx(ctx, tx, festID, actorID)
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

	// A host's Games: the row posts games_present_<uid> with a box per Game,
	// so ticking none — every Game — is told apart from a form without them.
	for userID, currentRole := range current {
		uid := idstr.Format(userID)
		if form.Get("games_present_"+uid) != "1" {
			continue
		}
		nextRole := roles.Normalize(form.Get("role_" + uid))
		if nextRole == "" {
			nextRole = currentRole
		}
		if nextRole != roles.Host || form.Get("delete_"+uid) == "1" {
			continue
		}
		var gameIDs []int64
		for _, raw := range form["games_"+uid] {
			if id, err := idstr.Parse(strings.TrimSpace(raw)); err == nil && id > 0 {
				gameIDs = append(gameIDs, id)
			}
		}
		if err := setHostGamesTx(ctx, tx, festID, userID, gameIDs); err != nil {
			return err
		}
	}

	addClicked := form.Get("add_access") == "1"
	nickname := strings.TrimSpace(form.Get("new_nickname"))
	if addClicked && nickname == "" {
		return corei18n.User(dopestrings.Default.Festaccess.Add.NicknameRequired())
	}
	if addClicked {
		role := roles.Normalize(form.Get("new_role"))
		if !roles.Assignable(role) {
			return corei18n.User(dopestrings.Default.Festaccess.Add.RoleInvalid())
		}
		userID, err := lookupUserIDByNicknameTx(ctx, tx, nickname)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return corei18n.User(dopestrings.Default.Festaccess.Add.UserNotFound(nickname))
			}
			return err
		}
		if userID == creatorID {
			return corei18n.User(dopestrings.Default.Festaccess.Add.CreatorExists())
		}
		if err := applyFestAccessMemberTx(ctx, tx, festID, creatorID, userID, current[userID], role, false, now); err != nil {
			return err
		}
	}
	return nil
}

// SaveFestAccessBulkTx applies access changes written as lines of
// "nickname:role" (or "nickname:remove") and says how many there were. The
// caller records the revision (fest:access).
func SaveFestAccessBulkTx(ctx context.Context, tx *sql.Tx, festID, actorID int64, raw string) (int, error) {
	changes, err := roles.ParseBulkLines(raw)
	if err != nil {
		return 0, err
	}
	if len(changes) == 0 {
		return 0, corei18n.User(dopestrings.Default.Festaccess.Bulk.Empty())
	}

	creatorID, current, err := accessEditTx(ctx, tx, festID, actorID)
	if err != nil {
		return 0, err
	}
	if err := applyBulkChangesTx(ctx, tx, festID, creatorID, current, changes); err != nil {
		return 0, err
	}
	return len(changes), nil
}

// accessEditTx checks the actor may manage the fest's access and returns the
// creator's id and every member's current role (user id → role).
func accessEditTx(ctx context.Context, tx *sql.Tx, festID, actorID int64) (int64, map[int64]string, error) {
	actorRole, err := FestUserRoleFromQuery(ctx, tx, festID, actorID)
	if err != nil {
		return 0, nil, err
	}
	if !roles.CanManageAccess(actorRole) {
		return 0, nil, corei18n.User(dopestrings.Default.Festaccess.Manage.Denied())
	}
	creatorID, err := syncFestCreatorAccessTx(ctx, tx, festID)
	if err != nil {
		return 0, nil, err
	}
	current, err := loadFestAccessRoleMapTx(ctx, tx, festID, creatorID)
	if err != nil {
		return 0, nil, err
	}
	return creatorID, current, nil
}

// applyBulkChangesTx applies the bulk form's lines in order, keeping current
// (user id → role) up to date as it goes.
func applyBulkChangesTx(ctx context.Context, tx *sql.Tx, festID, creatorID int64, current map[int64]string, changes []roles.BulkAccessLine) error {
	now := util.UtcNow()
	for _, change := range changes {
		userID, err := lookupUserIDByNicknameTx(ctx, tx, change.Nickname)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return corei18n.User(dopestrings.Default.Festaccess.Bulk.UserNotFound(strconv.Itoa(change.Line), change.Nickname))
			}
			return err
		}
		if err := applyFestAccessMemberTx(ctx, tx, festID, creatorID, userID, current[userID], change.Role, change.Delete, now); err != nil {
			return corei18n.User(dopestrings.Default.Festaccess.Bulk.LinePrefix(strconv.Itoa(change.Line), err.Error()))
		}
		if change.Delete {
			delete(current, userID)
		} else {
			current[userID] = change.Role
		}
	}
	return nil
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
			return corei18n.User(dopestrings.Default.Festaccess.Member.CreatorProtected())
		}
		_, err := tx.ExecContext(ctx, `
update fest_organizers set role = 'creator' where fest_id = ? and user_id = ?`, festID, userID)
		return err
	}
	if deleteMember {
		return deleteFestAccessMemberTx(ctx, tx, festID, userID)
	}
	if nextRole == "" {
		nextRole = currentRole
	}
	if !roles.Assignable(nextRole) {
		return corei18n.User(dopestrings.Default.Festaccess.Member.RoleInvalid())
	}
	_, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at)
values(?, ?, ?, ?)
on conflict(fest_id, user_id) do update set role = excluded.role`,
		festID, userID, nextRole, now)
	return err
}

// deleteFestAccessMemberTx removes a member from the fest and from every game
// they host there.
func deleteFestAccessMemberTx(ctx context.Context, tx *sql.Tx, festID, userID int64) error {
	if _, err := tx.ExecContext(ctx, `
delete from fest_game_hosts where fest_id = ? and user_id = ?`, festID, userID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
delete from fest_organizers where fest_id = ? and user_id = ?`, festID, userID)
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
	userID, err := store.UserIDByName(ctx, tx, nickname)
	if !errors.Is(err, sql.ErrNoRows) {
		return userID, err
	}
	return store.UserIDByTelegramName(ctx, tx, nickname)
}

// SetHostGamesTx sets, per host nickname, the Games that host may run, each named
// by its id, code or slug in this fest; an empty list is every Game. It is the
// API's twin of the Games boxes under a host on the dashboard, and like them
// only an admin or the creator may use it, and only on a host. The caller
// records the revision (fest:access).
func SetHostGamesTx(ctx context.Context, tx *sql.Tx, festID, actorID int64, games map[string][]string) error {
	s := dopestrings.Default
	actorRole, err := FestUserRoleFromQuery(ctx, tx, festID, actorID)
	if err != nil {
		return err
	}
	if !roles.CanManageAccess(actorRole) {
		return corei18n.User(s.Festaccess.Manage.Denied())
	}
	creatorID, err := syncFestCreatorAccessTx(ctx, tx, festID)
	if err != nil {
		return err
	}
	current, err := loadFestAccessRoleMapTx(ctx, tx, festID, creatorID)
	if err != nil {
		return err
	}
	for nickname, refs := range games {
		userID, err := lookupUserIDByNicknameTx(ctx, tx, nickname)
		if errors.Is(err, sql.ErrNoRows) {
			return corei18n.User(s.Festaccess.Add.UserNotFound(nickname))
		}
		if err != nil {
			return err
		}
		if current[userID] != roles.Host {
			return corei18n.User(s.Festaccess.Games.NotHost(nickname))
		}
		gameIDs := make([]int64, 0, len(refs))
		for _, ref := range refs {
			ref = strings.TrimSpace(ref)
			var gameID int64
			err := tx.QueryRowContext(ctx, `
select id from games where fest_id = ? and (cast(id as text) = ? or code = ? or slug = ?) limit 1`,
				festID, ref, ref, ref).Scan(&gameID)
			if errors.Is(err, sql.ErrNoRows) {
				return corei18n.User(s.Festaccess.Games.Unknown(ref))
			}
			if err != nil {
				return err
			}
			gameIDs = append(gameIDs, gameID)
		}
		if err := setHostGamesTx(ctx, tx, festID, userID, gameIDs); err != nil {
			return err
		}
	}
	return nil
}

// setHostGamesTx replaces the Games a host may run with gameIDs; none is every
// Game. An id that is not a Game of this fest is ignored.
func setHostGamesTx(ctx context.Context, tx *sql.Tx, festID, userID int64, gameIDs []int64) error {
	if _, err := tx.ExecContext(ctx, `delete from fest_game_hosts where fest_id = ? and user_id = ?`, festID, userID); err != nil {
		return err
	}
	for _, gameID := range gameIDs {
		if _, err := tx.ExecContext(ctx, `
insert or ignore into fest_game_hosts(fest_id, game_id, user_id)
select fest_id, id, ? from games where id = ? and fest_id = ?`, userID, gameID, festID); err != nil {
			return err
		}
	}
	return nil
}

// HostGamesByUser lists, per host of the fest, the Games they are limited to.
// A host missing from the map runs every Game.
func HostGamesByUser(ctx context.Context, q store.Queryer, festID int64) (map[int64][]int64, error) {
	rows, err := store.CollectRows(ctx, q, `
select user_id, game_id from fest_game_hosts where fest_id = ? order by user_id, game_id`,
		[]any{festID}, func(rs *sql.Rows) ([2]int64, error) {
			var pair [2]int64
			return pair, rs.Scan(&pair[0], &pair[1])
		})
	if err != nil {
		return nil, err
	}
	out := map[int64][]int64{}
	for _, pair := range rows {
		out[pair[0]] = append(out[pair[0]], pair[1])
	}
	return out, nil
}

// MayRunGame says whether a user with this role on the fest may edit this
// Game's tables. An admin or the creator may run every Game; a host may run
// every Game unless an admin limited them to some, and then only those.
func MayRunGame(ctx context.Context, q store.Queryer, festID, gameID, userID int64, role string) (bool, error) {
	if !roles.CanEditGameTables(role) {
		return false, nil
	}
	if roles.Normalize(role) != roles.Host || gameID <= 0 {
		return true, nil
	}
	var limited, here int
	err := q.QueryRowContext(ctx, `
select count(*), coalesce(sum(game_id = ?), 0) from fest_game_hosts where fest_id = ? and user_id = ?`,
		gameID, festID, userID).Scan(&limited, &here)
	if err != nil {
		return false, err
	}
	return limited == 0 || here > 0, nil
}
