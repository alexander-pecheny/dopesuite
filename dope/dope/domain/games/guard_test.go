package games

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// guardAllowed are the lines outside the registry that may still name a
// format by its code (ADR-0027), each with the reason it stays. A path that
// ends in "/" allows its whole directory. The guard fails on any other line,
// and on an allowed line that no longer exists, so the list only shrinks.
var guardAllowed = map[string][]string{
	// The registry itself: each format's Definition, Protocol and document.
	"domain/games/": nil,
	// Historical data conversions: they read rows as the codes were when the
	// conversion was written.
	"storage/migrate/": nil,
	// The fixture fest seeds one Game of each format and deals each its own
	// plausible document; it is test data that names what it builds.
	"domain/fixture/": nil,
	// storage may not import domain/games (ARCHITECTURE.md); a pasted EK-shaped
	// scheme must carry stages.
	"storage/storeutil/scheme_ops.go": {
		`if (gameType == "" || gameType == "ek" || gameType == "es") && len(scheme.Stages) == 0 {`,
	},
	// The roster import's result names the OD and KSI Games it updated, in
	// fields of the API answer and of the stored journal event.
	"domain/imports/handroster.go": {
		`case games.OD:`,
		`case games.KSI:`,
	},
	// The legacy seed source "the fest's first KSI": the EK page's import
	// button and the "ksi" keyword an old entrant list stores.
	"domain/imports/seed.go": {
		"select code from games where fest_id = ? and game_type = 'ksi' order by position, id limit 1`, scope.FestID).Scan(&code)",
	},
	"domain/entrants/entrants.go": {
		`case "ksi":`,
		"select code from games where fest_id = ? and game_type = 'ksi' order by position, id limit 1`, festID).Scan(&code); err != nil {",
	},
}

// TestNoFormatSwitchOutsideTheRegistry greps the server's non-test Go for a
// format code compared, switched on or filtered by in SQL. A format's facts
// live on its Definition and its Protocol; code elsewhere asks them.
func TestNoFormatSwitchOutsideTheRegistry(t *testing.T) {
	var codes, consts []string
	for _, d := range All() {
		codes = append(codes, regexp.QuoteMeta(d.Code))
	}
	consts = []string{"EK", "ES", "OD", "KSI", "SI", "Brain", "Multi", "Troika", "Hamsa", "KD"}
	if len(consts) != len(codes) {
		t.Fatalf("the guard names %d format constants, the registry has %d formats: add the new one's constant", len(consts), len(codes))
	}
	code := `"(?:` + strings.Join(codes, "|") + `)"`
	konst := `games\.(?:` + strings.Join(consts, "|") + `)\b`
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\bcase\s[^:]*(?:` + code + `|` + konst + `)`),
		regexp.MustCompile(`[=!]=\s*(?:` + code + `|` + konst + `)`),
		regexp.MustCompile(`(?:` + code + `|` + konst + `)\s*[=!]=`),
		regexp.MustCompile(`game_type\s*(?:=|in)\s*\(?[^)` + "`" + `]*'(?:` + strings.Join(codes, "|") + `)'`),
	}

	root := filepath.Join("..", "..")
	seen := map[string]map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_gen.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		lines, allowedFile := allowedLines(rel)
		if allowedFile {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
		for n := 1; scanner.Scan(); n++ {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "//") {
				continue
			}
			for _, p := range patterns {
				if !p.MatchString(line) {
					continue
				}
				if lines[line] {
					if seen[rel] == nil {
						seen[rel] = map[string]bool{}
					}
					seen[rel][line] = true
					break
				}
				t.Errorf("%s:%d names a format by its code: %s\nAsk the format's Definition or its Protocol (domain/games) instead (ADR-0027).", rel, n, line)
				break
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	for file, lines := range guardAllowed {
		if strings.HasSuffix(file, "/") {
			continue
		}
		for _, line := range lines {
			if !seen[file][line] {
				t.Errorf("%s no longer has the allowed line %q: take it off guardAllowed", file, line)
			}
		}
	}
}

// allowedLines is the set of lines a file may keep, and whether the whole
// file is allowed.
func allowedLines(rel string) (map[string]bool, bool) {
	for prefix := range guardAllowed {
		if strings.HasSuffix(prefix, "/") && strings.HasPrefix(rel, prefix) {
			return nil, true
		}
	}
	out := map[string]bool{}
	for _, line := range guardAllowed[rel] {
		out[line] = true
	}
	return out, false
}
