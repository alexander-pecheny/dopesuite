package spliffserver

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"pecheny.me/dopecore/authcred"
	"pecheny.me/dopecore/sqlitex"
)

// adduser mints a password account from the shell. Registration is otherwise
// telegram-only, and an instance with no bot — staging, a dev checkout, the
// verify run — would have no way in at all. The password comes from stdin so it
// never reaches the shell history:
//
//	printf '<password>' | SPLIFF_DB=… spliff-server adduser <username>
func runAddUser(args []string) {
	if len(args) != 1 {
		log.Fatal("usage: printf '<password>' | spliff-server adduser <username>")
	}
	username := strings.TrimSpace(args[0])
	if !validNewUsername(username) {
		log.Fatal("a username is 3-64 characters of letters, digits and ._-")
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1024))
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

	path := os.Getenv("SPLIFF_DB")
	if path == "" {
		path = dbFile
	}
	db, err := openDB(path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	now := rfc3339(time.Now())
	_, err = db.ExecContext(context.Background(), `
insert into users(username, password_hash, created_at, updated_at) values(?, ?, ?, ?)
on conflict(username) do update set password_hash = excluded.password_hash, updated_at = excluded.updated_at`,
		username, hash, now, now)
	if err != nil {
		if sqlitex.IsUniqueViolation(err) {
			log.Fatalf("could not write %s: %v", username, err)
		}
		log.Fatal(err)
	}
	fmt.Printf("password set for %s in %s\n", username, path)
}
