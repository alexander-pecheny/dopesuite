package imports

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"strconv"
	"strings"

	"dope/dope/domain/roster"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	"github.com/xuri/excelize/v2"
	corei18n "pecheny.me/dopecore/i18nstrings"
)

// The fest roster as a spreadsheet (ADR-0024): exported one row per person,
// and read back in the same layout as the host's edits. A team the sheet
// names is set to the people it lists; a team it does not name is left alone.

// rosterColumns are the sheet's columns in order, as the Catalog names them.
func rosterColumns() []string {
	return []string{
		dopestrings.Default.Imports.RosterXlsx.ColNumber(), dopestrings.Default.Imports.RosterXlsx.ColTeam(),
		dopestrings.Default.Imports.RosterXlsx.ColCity(), dopestrings.Default.Imports.RosterXlsx.ColTeamId(),
		dopestrings.Default.Imports.RosterXlsx.ColFlags(), dopestrings.Default.Imports.RosterXlsx.ColPlayer(),
		dopestrings.Default.Imports.RosterXlsx.ColPlayerId(),
	}
}

// BuildRosterXLSX is the fest roster as a workbook: a row per person, and a
// row for a team with nobody on it.
func BuildRosterXLSX(ctx context.Context, q store.Queryer, festID int64) ([]byte, error) {
	state, err := roster.LoadHandState(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	teams := roster.SortedFestRosterImportTeams(activeRoster(state))
	book := excelize.NewFile()
	defer book.Close()
	sheet := book.GetSheetName(0)
	write := func(row int, values []any) error {
		cell, err := excelize.CoordinatesToCellName(1, row)
		if err != nil {
			return err
		}
		return book.SetSheetRow(sheet, cell, &values)
	}
	header := make([]any, 0, 7)
	for _, col := range rosterColumns() {
		header = append(header, col)
	}
	if err := write(1, header); err != nil {
		return nil, err
	}
	row := 2
	for _, team := range teams {
		flags := strings.Join(roster.FlagShortNames(team.Flags), ", ")
		base := []any{team.Number, team.Name, team.City, optionalInt(team.RatingID), flags}
		if len(team.Players) == 0 {
			if err := write(row, append(base, "", "")); err != nil {
				return nil, err
			}
			row++
			continue
		}
		for _, p := range team.Players {
			values := append(append([]any{}, base...), store.JoinPlayerName(p.FirstName, p.LastName), optionalInt(p.RatingID))
			if err := write(row, values); err != nil {
				return nil, err
			}
			row++
		}
	}
	var out bytes.Buffer
	if err := book.Write(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func optionalInt(v int64) any {
	if v <= 0 {
		return ""
	}
	return v
}

// SheetTeam is one team as a roster sheet gives it.
type SheetTeam struct {
	RatingID int64
	Name     string
	City     string
	// Flags is nil when the sheet has no Flags column, which leaves a team's
	// Flags as they are.
	Flags   *string
	Players []roster.FestRosterImportPlayer
}

// ParseRosterXLSX reads a roster sheet: its first sheet, the header row naming
// the columns (in any order; only the team's is required), then a row per
// person. Rows of one team need not stand together: a team is its rating id,
// or its name and city.
func ParseRosterXLSX(r io.Reader) ([]SheetTeam, error) {
	book, err := excelize.OpenReader(r)
	if err != nil {
		return nil, corei18n.User(dopestrings.Default.Imports.RosterXlsx.Open(err.Error()))
	}
	defer book.Close()
	sheets := book.GetSheetList()
	if len(sheets) == 0 {
		return nil, corei18n.User(dopestrings.Default.Imports.RosterXlsx.Empty())
	}
	rows, err := book.GetRows(sheets[0])
	if err != nil {
		return nil, err
	}
	names := rosterColumns()
	col := map[int]int{}
	headerAt := -1
	for i, row := range rows {
		found := map[int]int{}
		for j, cell := range row {
			for k, name := range names {
				if strings.EqualFold(strings.TrimSpace(cell), name) {
					found[k] = j
				}
			}
		}
		if _, ok := found[1]; ok {
			col, headerAt = found, i
			break
		}
	}
	if headerAt < 0 {
		return nil, corei18n.User(dopestrings.Default.Imports.RosterXlsx.NoHeader(names[1]))
	}
	get := func(row []string, k int) string {
		j, ok := col[k]
		if !ok || j >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[j])
	}
	_, hasFlags := col[4]
	var out []SheetTeam
	index := map[string]int{}
	for i := headerAt + 1; i < len(rows); i++ {
		row := rows[i]
		name := get(row, 1)
		if name == "" {
			continue
		}
		ratingID, _ := strconv.ParseInt(get(row, 3), 10, 64)
		city := get(row, 2)
		key := "name:" + strings.ToLower(name) + "|" + strings.ToLower(city)
		if ratingID > 0 {
			key = "rating:" + strconv.FormatInt(ratingID, 10)
		}
		at, seen := index[key]
		if !seen {
			team := SheetTeam{RatingID: ratingID, Name: name, City: city}
			if hasFlags {
				flags := get(row, 4)
				team.Flags = &flags
			}
			out = append(out, team)
			at = len(out) - 1
			index[key] = at
		}
		person := get(row, 5)
		if person == "" {
			continue
		}
		words := strings.Fields(person)
		playerID, _ := strconv.ParseInt(get(row, 6), 10, 64)
		out[at].Players = append(out[at].Players, roster.FestRosterImportPlayer{
			RatingID: playerID, FirstName: words[0], LastName: strings.Join(words[1:], " "),
		})
	}
	if len(out) == 0 {
		return nil, corei18n.User(dopestrings.Default.Imports.RosterXlsx.NoTeams())
	}
	return out, nil
}

// ApplyRosterSheetTx sets every team the sheet names: an existing one, found
// by its rating id or else its name and city, takes the sheet's name, city,
// people and Flags as the host's edits; a team the fest does not have is made
// by hand. Teams the sheet does not name are left alone. It returns what the
// roster was and is, for the preview.
func ApplyRosterSheetTx(ctx context.Context, tx *sql.Tx, festID int64, teams []SheetTeam) (ImportPlan, RosterWrite, error) {
	state, err := roster.LoadHandState(ctx, tx, festID)
	if err != nil {
		return ImportPlan{}, RosterWrite{}, err
	}
	before := activeRoster(state)
	var written RosterWrite
	for _, team := range teams {
		rowID := int64(0)
		for _, row := range state.Teams {
			if row.Deleted {
				continue
			}
			if team.RatingID > 0 && row.RatingID == team.RatingID {
				rowID = row.ID
				break
			}
			if team.RatingID <= 0 && strings.EqualFold(row.Name, team.Name) && strings.EqualFold(row.City, team.City) {
				rowID = row.ID
			}
		}
		in := TeamInput{RatingID: team.RatingID, Name: team.Name, City: team.City, Players: team.Players}
		if rowID > 0 {
			if err := setSheetFlagsTx(ctx, tx, festID, rowID, team.Flags); err != nil {
				return ImportPlan{}, RosterWrite{}, err
			}
			if written, err = SaveHandTeamTx(ctx, tx, festID, rowID, in); err != nil {
				return ImportPlan{}, RosterWrite{}, corei18n.User(dopestrings.Default.Imports.RosterXlsx.TeamFailed(team.Name, userMessage(err)))
			}
		} else {
			id, w, err := CreateHandTeamTx(ctx, tx, festID, in)
			if err != nil {
				return ImportPlan{}, RosterWrite{}, corei18n.User(dopestrings.Default.Imports.RosterXlsx.TeamFailed(team.Name, userMessage(err)))
			}
			written = w
			if team.Flags != nil && strings.TrimSpace(*team.Flags) != "" {
				if err := setSheetFlagsTx(ctx, tx, festID, id, team.Flags); err != nil {
					return ImportPlan{}, RosterWrite{}, err
				}
				if written, err = rewriteFromHandTx(ctx, tx, festID); err != nil {
					return ImportPlan{}, RosterWrite{}, err
				}
			}
		}
		if state, err = roster.LoadHandState(ctx, tx, festID); err != nil {
			return ImportPlan{}, RosterWrite{}, err
		}
	}
	after := activeRoster(state)
	return planRoster(before, after, roster.MergeResult{}), written, nil
}

// setSheetFlagsTx gives a team the sheet's Flags, as the host's, when the
// sheet has the column and they differ.
func setSheetFlagsTx(ctx context.Context, tx *sql.Tx, festID, teamID int64, typed *string) error {
	if typed == nil {
		return nil
	}
	var flags []roster.FestRosterFlag
	for _, part := range strings.Split(*typed, ",") {
		flags = append(flags, roster.FestRosterFlag{Short: part, Full: part})
	}
	flags = roster.NormalizeFlags(flags)
	now, err := roster.LoadFestTeamFlags(ctx, tx, festID)
	if err != nil {
		return err
	}
	if strings.Join(roster.FlagShortNames(now[teamID]), ",") == strings.Join(roster.FlagShortNames(flags), ",") {
		return nil
	}
	if err := roster.ReplaceTeamFlagsTx(ctx, tx, teamID, flags); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `update fest_teams set hand_flags = 1 where id = ? and fest_id = ?`, teamID, festID)
	return err
}

// userMessage is an error as a person may read it.
func userMessage(err error) string {
	msg, _ := corei18n.Reveal(err, dopestrings.Default.Imports.RosterXlsx.Unknown())
	return msg
}
