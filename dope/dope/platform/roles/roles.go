// Package roles holds the pure fest-permission model: the role hierarchy and
// the predicates that decide what each role may do, plus the bulk-access line
// parser. It is a leaf — it depends only on the standard library and the
// shared Catalog, never on the server, database or HTTP layers — so the
// permission rules have a single, independently-testable home. The DB-backed
// access management (loading and persisting fest_organizers rows) stays in
// package main as a thin layer that consults these predicates.
package roles

import (
	"strconv"
	"strings"

	core "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
	dopestrings "dope/i18nstrings"
)

// Canonical fest roles, ordered creator > admin > host. Stored verbatim in the
// fest_organizers.role column. GameJury stands apart: it rules on a Game's
// contested answers and edits nothing.
const (
	Creator  = "creator"
	Admin    = "admin"
	Host     = "host"
	GameJury = "game_jury"
)

// Normalize trims and validates a role string, returning "" for anything that
// is not one of the canonical roles.
func Normalize(role string) string {
	switch role = strings.TrimSpace(role); role {
	case Creator, Admin, Host, GameJury:
		return role
	default:
		return ""
	}
}

// CanManageFest reports whether the role may edit fest settings (creator/admin).
func CanManageFest(role string) bool {
	role = Normalize(role)
	return role == Creator || role == Admin
}

// CanManageAccess reports whether the role may change the fest's access list.
func CanManageAccess(role string) bool {
	return CanManageFest(role)
}

// CanDeleteFest reports whether the role may delete the fest (creator only).
func CanDeleteFest(role string) bool {
	return Normalize(role) == Creator
}

// CanEditGameTables reports whether the role may edit game tables (any role
// but the jury's).
func CanEditGameTables(role string) bool {
	role = Normalize(role)
	return role != "" && role != GameJury
}

// CanJudge reports whether the role may rule on contested answers: an admin's, the
// creator's, or the jury's in the Games it is not limited away from.
func CanJudge(role string) bool {
	return CanManageFest(role) || Normalize(role) == GameJury
}

// Assignable reports whether the role may be granted via the access UI; the
// creator role is implicit (fest ownership) and never hand-assigned.
func Assignable(role string) bool {
	role = Normalize(role)
	return role == Admin || role == Host || role == GameJury
}

// BulkAccessLine is one parsed line of the bulk access-grant textarea.
type BulkAccessLine struct {
	Line     int
	Nickname string
	Role     string
	Delete   bool
}

// ParseBulkLines parses the "username:role" bulk access input, one entry per
// line. "username:remove" marks a deletion. Blank lines are skipped. The error
// messages are line-numbered and shown to the host verbatim.
func ParseBulkLines(raw string) ([]BulkAccessLine, error) {
	var out []BulkAccessLine
	for idx, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		change, err := parseBulkLine(line, idx+1)
		if err != nil {
			return nil, err
		}
		out = append(out, change)
	}
	return out, nil
}

// parseBulkLine parses one non-blank "username:role" line.
func parseBulkLine(line string, lineNo int) (BulkAccessLine, error) {
	s := dopestrings.Default
	nickname, action, ok := strings.Cut(line, ":")
	if !ok {
		return BulkAccessLine{}, core.User(s.Roles.Bulk.FormatExpected(strconv.Itoa(lineNo)))
	}
	nickname = strings.TrimSpace(nickname)
	action = strings.ToLower(strings.TrimSpace(action))
	if nickname == "" || action == "" {
		return BulkAccessLine{}, core.User(s.Roles.Bulk.FormatExpected(strconv.Itoa(lineNo)))
	}
	change := BulkAccessLine{Line: lineNo, Nickname: nickname}
	switch action {
	case Admin, Host, GameJury:
		change.Role = action
	case "remove":
		change.Delete = true
	default:
		return BulkAccessLine{}, core.User(s.Roles.Bulk.ActionUnknown(strconv.Itoa(lineNo)))
	}
	return change, nil
}
