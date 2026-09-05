package venues

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/platform/util"
	"dope/dope/storage/store"

	dopestrings "dope/i18nstrings"
)

type Venue struct {
	ID            int64
	Slug          string
	Title         string
	City          string
	Description   string
	RatingVenueID int64
	IsPublic      bool
}

func (v Venue) Ref() string {
	if v.Slug != "" {
		return v.Slug
	}
	return strconv.FormatInt(v.ID, 10)
}

type Slot struct {
	ID                 int64
	FestID             int64
	GameID             int64
	StartsAt           string
	RatingTournamentID int64
	RegToken           string
	RegOpensAt         string
	RegClosesAt        string
	RegShut            bool
	LinkVisible        bool
	GameSlug           string
	Accepted           int
	Pending            int
}

// GameRef is how a Slot's Game is named in a URL: its slug when it has one,
// its id otherwise.
func (s Slot) GameRef() string {
	if s.GameSlug != "" {
		return s.GameSlug
	}
	return strconv.FormatInt(s.GameID, 10)
}

type Application struct {
	ID            int64
	SlotID        int64
	UserID        int64
	Status        string
	ParticipantID int64
	CreatedAt     string
	UpdatedAt     string
	Submitter     string
	SubmitterTgID int64
	Seq           int64
	TeamName      string
	RatingTeamID  int64
	Roster        []RosterPlayer
	Number        int64
}

type Version struct {
	ID           int64
	Seq          int64
	TeamName     string
	RatingTeamID int64
	Roster       []RosterPlayer
	Author       string
	CreatedAt    string
}

const venueSelect = `
select f.id, coalesce(f.slug, ''), f.title, coalesce(f.city, ''), f.description,
       coalesce(f.rating_venue_id, 0), f.is_public
from fests f where f.kind = 'venue' and `

func scanVenue(row interface{ Scan(...any) error }) (Venue, error) {
	var v Venue
	var public int
	err := row.Scan(&v.ID, &v.Slug, &v.Title, &v.City, &v.Description, &v.RatingVenueID, &public)
	v.IsPublic = public == 1
	return v, err
}

func LoadVenue(ctx context.Context, q store.Queryer, festID int64) (Venue, error) {
	return scanVenue(q.QueryRowContext(ctx, venueSelect+`f.id = ?`, festID))
}

func IsVenue(ctx context.Context, q store.Queryer, festID int64) bool {
	var kind string
	err := q.QueryRowContext(ctx, `select kind from fests where id = ?`, festID).Scan(&kind)
	return err == nil && kind == KindVenue
}

func PublicVenues(ctx context.Context, q store.Queryer) ([]Venue, error) {
	return store.CollectRows(ctx, q, venueSelect+`f.is_public = 1 order by f.title, f.id`, nil,
		func(rows *sql.Rows) (Venue, error) { return scanVenue(rows) })
}

const slotSelect = `
select s.id, s.fest_id, s.game_id, coalesce(s.starts_at, ''), coalesce(s.rating_tournament_id, 0),
       s.reg_token, coalesce(s.reg_opens_at, ''), coalesce(s.reg_closes_at, ''), s.reg_closed, s.link_visible, coalesce(g.slug, ''),
       (select count(*) from slot_applications a where a.slot_id = s.id and a.status = 'accepted'),
       (select count(*) from slot_applications a where a.slot_id = s.id and a.status = 'pending')
from slots s join games g on g.id = s.game_id where `

func scanSlot(row interface{ Scan(...any) error }) (Slot, error) {
	var s Slot
	var closed, visible int
	err := row.Scan(&s.ID, &s.FestID, &s.GameID, &s.StartsAt, &s.RatingTournamentID,
		&s.RegToken, &s.RegOpensAt, &s.RegClosesAt, &closed, &visible, &s.GameSlug, &s.Accepted, &s.Pending)
	s.RegShut = closed == 1
	s.LinkVisible = visible == 1
	return s, err
}

func LoadSlot(ctx context.Context, q store.Queryer, slotID int64) (Slot, error) {
	return scanSlot(q.QueryRowContext(ctx, slotSelect+`s.id = ?`, slotID))
}

func SlotByGameID(ctx context.Context, q store.Queryer, gameID int64) (Slot, error) {
	return scanSlot(q.QueryRowContext(ctx, slotSelect+`s.game_id = ?`, gameID))
}

func SlotByToken(ctx context.Context, q store.Queryer, token string) (Slot, error) {
	if strings.TrimSpace(token) == "" {
		return Slot{}, sql.ErrNoRows
	}
	return scanSlot(q.QueryRowContext(ctx, slotSelect+`s.reg_token = ?`, token))
}

func VenueSlots(ctx context.Context, q store.Queryer, festID int64) ([]Slot, error) {
	return store.CollectRows(ctx, q, slotSelect+`
s.fest_id = ?
order by case when s.starts_at = '' then 1 else 0 end, s.starts_at, s.id`, []any{festID},
		func(rows *sql.Rows) (Slot, error) { return scanSlot(rows) })
}

// CreateSlotTx makes a Slot whose registration is already running: the link
// exists from the first moment and works from it, so a Representative can hand
// it to a team before touching anything else. What waits for them is the link
// on the Venue's public page, which is off until they tick it.
func CreateSlotTx(ctx context.Context, tx *sql.Tx, festID int64, startsAt string, tournamentID int64, opensAt string, tourComp []int) (int64, error) {
	gameID, err := gamebuild.Create(ctx, tx, gamebuild.Spec{
		FestID: festID, Type: games.OD, ODTourComp: tourComp, OwnTeams: true,
	})
	if err != nil {
		return 0, err
	}
	now := util.UtcNow()
	return store.InsertReturningID(ctx, tx, `
insert into slots(fest_id, game_id, starts_at, rating_tournament_id, reg_token, reg_opens_at, reg_closed, link_visible, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, 0, 0, ?, ?)`,
		festID, gameID, FormatTime(startsAt), util.NullableInt64(tournamentID), NewToken(),
		util.NullableString(FormatTime(opensAt)), now, now)
}

func UpdateSlotTx(ctx context.Context, tx *sql.Tx, slotID int64, startsAt string, tournamentID int64) error {
	_, err := tx.ExecContext(ctx, `
update slots set starts_at = ?, rating_tournament_id = ?, updated_at = ? where id = ?`,
		FormatTime(startsAt), util.NullableInt64(tournamentID), util.UtcNow(), slotID)
	return err
}

// UpdateSlotRegTx is what the registration dialog saves: the window it runs on
// and whether the Venue's page carries the link. It reopens a registration a
// Representative had shut.
func UpdateSlotRegTx(ctx context.Context, tx *sql.Tx, slotID int64, opensAt, closesAt string, linkVisible bool) error {
	_, err := tx.ExecContext(ctx, `
update slots set reg_opens_at = ?, reg_closes_at = ?, link_visible = ?, updated_at = ? where id = ?`,
		util.NullableString(FormatTime(opensAt)), util.NullableString(FormatTime(closesAt)),
		util.BoolToInt(linkVisible), util.UtcNow(), slotID)
	return err
}

// ShutSlotRegTx closes a registration on the spot, or opens one that was shut.
// It is the Representative's own hand on it, apart from the window: a Слот that
// filled up tonight is shut tonight, not by editing a date to be in the past.
func ShutSlotRegTx(ctx context.Context, tx *sql.Tx, slotID int64, shut bool) error {
	_, err := tx.ExecContext(ctx, `update slots set reg_closed = ?, updated_at = ? where id = ?`,
		util.BoolToInt(shut), util.UtcNow(), slotID)
	return err
}

func NewTokenTx(ctx context.Context, tx *sql.Tx, slotID int64) error {
	_, err := tx.ExecContext(ctx, `update slots set reg_token = ?, updated_at = ? where id = ?`,
		NewToken(), util.UtcNow(), slotID)
	return err
}

const applicationSelect = `
select a.id, a.slot_id, a.user_id, a.status, coalesce(a.participant_id, 0), a.created_at, a.updated_at,
       coalesce(nullif(u.telegram_username, ''), nullif(u.username, ''), ''),
       coalesce(u.telegram_user_id, 0),
       coalesce(v.seq, 0), coalesce(v.team_name, ''), coalesce(v.rating_team_id, 0), coalesce(v.roster_json, '[]'),
       coalesce(gp.number, 0)
from slot_applications a
join users u on u.id = a.user_id
join slots sl on sl.id = a.slot_id
left join game_participants gp on gp.participant_id = a.participant_id and gp.game_id = sl.game_id
left join slot_application_versions v on v.application_id = a.id
  and v.seq = (select max(seq) from slot_application_versions w where w.application_id = a.id)
where `

func scanApplication(row interface{ Scan(...any) error }) (Application, error) {
	var a Application
	var roster string
	err := row.Scan(&a.ID, &a.SlotID, &a.UserID, &a.Status, &a.ParticipantID, &a.CreatedAt, &a.UpdatedAt,
		&a.Submitter, &a.SubmitterTgID, &a.Seq, &a.TeamName, &a.RatingTeamID, &roster, &a.Number)
	a.Roster = ParseRoster(roster)
	return a, err
}

func SlotApplications(ctx context.Context, q store.Queryer, slotID int64) ([]Application, error) {
	return store.CollectRows(ctx, q, applicationSelect+`a.slot_id = ? order by a.created_at, a.id`, []any{slotID},
		func(rows *sql.Rows) (Application, error) { return scanApplication(rows) })
}

func UserApplication(ctx context.Context, q store.Queryer, slotID, userID int64) (Application, error) {
	return scanApplication(q.QueryRowContext(ctx, applicationSelect+`a.slot_id = ? and a.user_id = ?`, slotID, userID))
}

func LoadApplication(ctx context.Context, q store.Queryer, appID int64) (Application, error) {
	return scanApplication(q.QueryRowContext(ctx, applicationSelect+`a.id = ?`, appID))
}

func ApplicationVersions(ctx context.Context, q store.Queryer, appID int64) ([]Version, error) {
	return store.CollectRows(ctx, q, `
select v.id, v.seq, v.team_name, coalesce(v.rating_team_id, 0), v.roster_json,
       coalesce(nullif(u.telegram_username, ''), nullif(u.username, ''), ''), v.created_at
from slot_application_versions v
left join users u on u.id = v.created_by
where v.application_id = ?
order by v.seq desc`, []any{appID}, func(rows *sql.Rows) (Version, error) {
		var v Version
		var roster string
		err := rows.Scan(&v.ID, &v.Seq, &v.TeamName, &v.RatingTeamID, &roster, &v.Author, &v.CreatedAt)
		v.Roster = ParseRoster(roster)
		return v, err
	})
}

var ErrNoTeamName = errors.New(dopestrings.Default.Venues.Errors.NoTeamName())

func SaveVersionTx(ctx context.Context, tx *sql.Tx, slotID, userID, authorID int64, teamName string, ratingTeamID int64, roster []RosterPlayer) (int64, error) {
	teamName = strings.TrimSpace(teamName)
	if teamName == "" {
		return 0, ErrNoTeamName
	}
	now := util.UtcNow()
	var appID int64
	err := tx.QueryRowContext(ctx, `select id from slot_applications where slot_id = ? and user_id = ?`, slotID, userID).Scan(&appID)
	if errors.Is(err, sql.ErrNoRows) {
		if appID, err = store.InsertReturningID(ctx, tx, `
insert into slot_applications(slot_id, user_id, status, created_at, updated_at)
values(?, ?, 'pending', ?, ?)`, slotID, userID, now, now); err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	} else if _, err := tx.ExecContext(ctx, `update slot_applications set updated_at = ? where id = ?`, now, appID); err != nil {
		return 0, err
	}
	var seq int64
	if err := tx.QueryRowContext(ctx,
		`select coalesce(max(seq), 0) + 1 from slot_application_versions where application_id = ?`, appID).Scan(&seq); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
insert into slot_application_versions(application_id, seq, team_name, rating_team_id, roster_json, created_by, created_at)
values(?, ?, ?, ?, ?, ?, ?)`, appID, seq, teamName, ratingTeamID, MarshalRoster(roster), authorID, now); err != nil {
		return 0, err
	}
	return appID, nil
}

func VenuesOf(ctx context.Context, q store.Queryer, userID int64) ([]Venue, error) {
	return store.CollectRows(ctx, q, venueSelect+`
f.id in (select fest_id from fest_organizers where user_id = ?)
order by f.title, f.id`, []any{userID}, func(rows *sql.Rows) (Venue, error) { return scanVenue(rows) })
}

func NextSlots(ctx context.Context, q store.Queryer, day string) (map[int64]Slot, error) {
	rows, err := store.CollectRows(ctx, q, slotSelect+`
s.starts_at >= ?
order by s.fest_id, s.starts_at, s.id`, []any{day},
		func(rows *sql.Rows) (Slot, error) { return scanSlot(rows) })
	if err != nil {
		return nil, err
	}
	out := map[int64]Slot{}
	for _, slot := range rows {
		if _, seen := out[slot.FestID]; !seen {
			out[slot.FestID] = slot
		}
	}
	return out, nil
}

// RegistryCities are the Venue's registry rows' towns, keyed by rating team id.
func RegistryCities(ctx context.Context, q store.Queryer, festID int64) (map[int64]string, error) {
	rows, err := store.CollectRows(ctx, q, `
select rating_id, city from fest_teams where fest_id = ? and rating_id is not null and city <> ''`,
		[]any{festID}, func(rows *sql.Rows) ([2]any, error) {
			var id int64
			var city string
			err := rows.Scan(&id, &city)
			return [2]any{id, city}, err
		})
	if err != nil {
		return nil, err
	}
	out := map[int64]string{}
	for _, row := range rows {
		out[row[0].(int64)] = row[1].(string)
	}
	return out, nil
}
