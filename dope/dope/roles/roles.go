// Package roles holds the pure fest-permission model: the role hierarchy and
// the predicates that decide what each role may do, plus the bulk-access line
// parser. It is a leaf — it depends only on the standard library and never on
// the server, database or HTTP layers — so the permission rules have a single,
// independently-testable home. The DB-backed access management (loading and
// persisting fest_organizers rows) stays in package main as a thin layer that
// consults these predicates.
package roles

import (
	"fmt"
	"strings"
)

// Canonical fest roles, ordered creator > admin > host. Stored verbatim in the
// fest_organizers.role column.
const (
	Creator = "creator"
	Admin   = "admin"
	Host    = "host"
)

// Normalize trims and validates a role string, returning "" for anything that
// is not one of the three canonical roles.
func Normalize(role string) string {
	switch strings.TrimSpace(role) {
	case Creator:
		return Creator
	case Admin:
		return Admin
	case Host:
		return Host
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

// CanEditGameTables reports whether the role may edit game tables (any role).
func CanEditGameTables(role string) bool {
	return Normalize(role) != ""
}

// Assignable reports whether the role may be granted via the access UI; the
// creator role is implicit (fest ownership) and never hand-assigned.
func Assignable(role string) bool {
	role = Normalize(role)
	return role == Admin || role == Host
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
		lineNo := idx + 1
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		nickname, action, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("строка %d: нужен формат username:role", lineNo)
		}
		nickname = strings.TrimSpace(nickname)
		action = strings.ToLower(strings.TrimSpace(action))
		if nickname == "" || action == "" {
			return nil, fmt.Errorf("строка %d: нужен формат username:role", lineNo)
		}
		change := BulkAccessLine{Line: lineNo, Nickname: nickname}
		switch action {
		case Admin, Host:
			change.Role = action
		case "remove":
			change.Delete = true
		default:
			return nil, fmt.Errorf("строка %d: действие должно быть host, admin или remove", lineNo)
		}
		out = append(out, change)
	}
	return out, nil
}
