package venues

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"dope/dope/platform/util"
	"dope/dope/storage/store"
)

// Venue is a Fest of kind 'venue'.
type Venue struct {
	ID            int64
	Slug          string
	Title         string
	City          string
	Description   string
	RatingVenueID int64
	IsPublic      bool
}

// Ref is how a Venue is named in a URL.
func (v Venue) Ref() string {
	if v.Slug != "" {
		return v.Slug
	}
	return strconv.FormatInt(v.ID, 10)
}

// Slot is one dated sitting at a Venue, realised as one Game.
type Slot struct {
	ID                 int64
	FestID             int64
	GameID             int64
	StartsAt           string
	RatingTournamentID int64
	RegToken           string
	RegOpensAt         string
	RegClosed          bool
	// GameSlug and TournamentName are joined in for the pages.
	GameSlug       string
	TournamentName string
	Accepted       int
	Pending        int
}

// Application is one Заявка with its current version folded in.
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

// Version is one dated edit of a Заявка.
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

// LoadVenue reads one Venue by fest id; sql.ErrNoRows when the fest is not one.
func LoadVenue(ctx context.Context, q store.Queryer, festID int64) (Venue, error) {
	return scanVenue(q.QueryRowContext(ctx, venueSelect+`f.id = ?`, festID))
}

// PublicVenues are the venues the /venues index lists.
func PublicVenues(ctx context.Context, q store.Queryer) ([]Venue, error) {
	return store.CollectRows(ctx, q, venueSelect+`f.is_public = 1 order by f.title, f.id`, nil,
		func(rows *sql.Rows) (Venue, error) { return scanVenue(rows) })
}

const slotSelect = `
select s.id, s.fest_id, s.game_id, coalesce(s.starts_at, ''), coalesce(s.rating_tournament_id, 0),
       s.reg_token, coalesce(s.reg_opens_at, ''), s.reg_closed, coalesce(g.slug, ''),
       (select count(*) from slot_applications a where a.slot_id = s.id and a.status = 'accepted'),
       (select count(*) from slot_applications a where a.slot_id = s.id and a.status = 'pending')
from slots s join games g on g.id = s.game_id where `

func scanSlot(row interface{ Scan(...any) error }) (Slot, error) {
	var s Slot
	var closed int
	err := row.Scan(&s.ID, &s.FestID, &s.GameID, &s.StartsAt, &s.RatingTournamentID,
		&s.RegToken, &s.RegOpensAt, &closed, &s.GameSlug, &s.Accepted, &s.Pending)
	s.RegClosed = closed == 1
	return s, err
}

// LoadSlot reads one Слот by id.
func LoadSlot(ctx context.Context, q store.Queryer, slotID int64) (Slot, error) {
	return scanSlot(q.QueryRowContext(ctx, slotSelect+`s.id = ?`, slotID))
}

// SlotByToken reads the Слот a registration link names.
func SlotByToken(ctx context.Context, q store.Queryer, token string) (Slot, error) {
	if strings.TrimSpace(token) == "" {
		return Slot{}, sql.ErrNoRows
	}
	return scanSlot(q.QueryRowContext(ctx, slotSelect+`s.reg_token = ?`, token))
}

// VenueSlots are a Venue's Слоты, soonest first, undated last.
func VenueSlots(ctx context.Context, q store.Queryer, festID int64) ([]Slot, error) {
	return store.CollectRows(ctx, q, slotSelect+`
s.fest_id = ?
order by case when s.starts_at = '' then 1 else 0 end, s.starts_at, s.id`, []any{festID},
		func(rows *sql.Rows) (Slot, error) { return scanSlot(rows) })
}

// CreateSlotTx records a Слот over a Game just built for it.
func CreateSlotTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, startsAt string, tournamentID int64, opensAt string) (int64, error) {
	now := util.UtcNow()
	return store.InsertReturningID(ctx, tx, `
insert into slots(fest_id, game_id, starts_at, rating_tournament_id, reg_token, reg_opens_at, reg_closed, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		festID, gameID, FormatTime(startsAt), util.NullableInt64(tournamentID), NewToken(),
		util.NullableString(FormatTime(opensAt)), now, now)
}

// UpdateSlotTx saves the header fields of a Слот.
func UpdateSlotTx(ctx context.Context, tx *sql.Tx, slotID int64, startsAt string, tournamentID int64, opensAt string, closed bool) error {
	_, err := tx.ExecContext(ctx, `
update slots set starts_at = ?, rating_tournament_id = ?, reg_opens_at = ?, reg_closed = ?, updated_at = ?
where id = ?`, FormatTime(startsAt), util.NullableInt64(tournamentID),
		util.NullableString(FormatTime(opensAt)), util.BoolToInt(closed), util.UtcNow(), slotID)
	return err
}

// NewTokenTx re-mints a Слот's registration link.
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
       coalesce(p.number, 0)
from slot_applications a
join users u on u.id = a.user_id
left join participants p on p.id = a.participant_id
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

// SlotApplications are a Слот's Заявки in filing order.
func SlotApplications(ctx context.Context, q store.Queryer, slotID int64) ([]Application, error) {
	return store.CollectRows(ctx, q, applicationSelect+`a.slot_id = ? order by a.created_at, a.id`, []any{slotID},
		func(rows *sql.Rows) (Application, error) { return scanApplication(rows) })
}

// UserApplication is a user's own Заявка on a Слот; sql.ErrNoRows when none.
func UserApplication(ctx context.Context, q store.Queryer, slotID, userID int64) (Application, error) {
	return scanApplication(q.QueryRowContext(ctx, applicationSelect+`a.slot_id = ? and a.user_id = ?`, slotID, userID))
}

// LoadApplication reads one Заявка by id.
func LoadApplication(ctx context.Context, q store.Queryer, appID int64) (Application, error) {
	return scanApplication(q.QueryRowContext(ctx, applicationSelect+`a.id = ?`, appID))
}

// ApplicationVersions are every edit of a Заявка, newest first.
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

// ErrNoTeamName refuses a заявка with nothing to call the team.
var ErrNoTeamName = errors.New("укажите название команды")

// SaveVersionTx files or edits a Заявка: the row when it is the first, then a
// version of its own. authorID is who typed it — the Representative when they
// edit someone else's заявка.
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
