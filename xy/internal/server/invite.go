package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"time"

	"pecheny.me/dopecore/authcred"
)

// mintInvite creates a one-shot invite valid for the given duration and returns
// the code.
func (s *server) mintInvite(ctx context.Context, ttl time.Duration) (string, error) {
	code, err := authcred.NewInviteCode()
	if err != nil {
		return "", err
	}
	now := time.Now()
	err = s.withWriteTx(ctx, "mint-invite", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
insert into invites(code, created_at, expires_at) values(?, ?, ?)`,
			code, rfc3339(now), rfc3339(now.Add(ttl)))
		return err
	})
	return code, err
}

// runMintInvite is the `xy-server invite [days]` subcommand.
func runMintInvite(args []string) {
	days := 7
	if len(args) > 0 {
		if d, err := strconv.Atoi(args[0]); err == nil && d > 0 {
			days = d
		}
	}
	srv, err := newServer()
	if err != nil {
		log.Fatal(err)
	}
	code, err := srv.mintInvite(context.Background(), time.Duration(days)*24*time.Hour)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("invite code: %s  (valid %d days)\n", code, days)
}
