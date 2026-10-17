package qyserver

import (
	"context"
	"database/sql"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/authcred"
	"code.pecheny.me/pecheny/dopesuite/dopecore/sqlitex"
)

// maxPasswordInput caps what adduser reads from stdin.
const maxPasswordInput = 1 << 10

// adduser mints a password account from the shell. Registration is otherwise
// telegram-only, and an instance with no bot — staging, a dev checkout, the
// verify run — would have no way in at all. The password comes from stdin so it
// never reaches the shell history:
//
//	printf '<password>' | QY_DB=… qy-server adduser <username>
func runAddUser(args []string) {
	if len(args) != 1 {
		log.Fatal("usage: printf '<password>' | qy-server adduser <username>")
	}
	username := strings.TrimSpace(args[0])
	if !validNewUsername(username) {
		log.Fatal("a username is 3-64 characters of letters, digits and ._-")
	}
	hash := readPasswordHash()

	path := os.Getenv("QY_DB")
	if path == "" {
		path = dbFile
	}
	db, err := openDB(path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	upsertPassword(db, username, hash)
	log.Printf("password set for %s in %s", username, path)
}

// readPasswordHash reads the password from stdin and hashes it.
func readPasswordHash() string {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, maxPasswordInput))
	if err != nil {
		log.Fatal(err)
	}
	password := strings.TrimRight(string(raw), "\r\n")
	if len(password) < authcred.PasswordMinLen || len(password) > authcred.PasswordMaxLen {
		log.Fatalf("a password is %d-%d characters; pipe it in on stdin",
			authcred.PasswordMinLen, authcred.PasswordMaxLen)
	}
	hash, err := authcred.HashPassword(password)
	if err != nil {
		log.Fatal(err)
	}
	return hash
}

// upsertPassword creates the account, or sets the password of an existing one.
func upsertPassword(db *sql.DB, username, hash string) {
	now := rfc3339(time.Now())
	_, err := db.ExecContext(context.Background(), `
insert into users(username, password_hash, created_at, updated_at) values(?, ?, ?, ?)
on conflict(username) do update set password_hash = excluded.password_hash, updated_at = excluded.updated_at`,
		username, hash, now, now)
	if err != nil {
		if sqlitex.IsUniqueViolation(err) {
			log.Fatalf("could not write %s: %v", username, err)
		}
		log.Fatal(err)
	}
}
