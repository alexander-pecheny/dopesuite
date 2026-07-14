package server

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// addUser provisions a username+password account directly, bypassing the
// invite/telegram registration flow. Used to bootstrap the first account on a
// fresh deploy. Returns an error if the username is taken.
func (s *server) addUser(ctx context.Context, username, password string) error {
	if len(username) < 3 {
		return errors.New("username too short")
	}
	if len(password) < passwordMinLen || len(password) > passwordMaxLen {
		return fmt.Errorf("password must be %d-%d chars", passwordMinLen, passwordMaxLen)
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	now := time.Now()
	return s.withWriteTx(ctx, "add-user", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
insert into users(username, password_hash, created_at, updated_at) values(?, ?, ?, ?)`,
			username, hash, rfc3339(now), rfc3339(now))
		if err != nil && strings.Contains(err.Error(), "UNIQUE") {
			return errors.New("username already taken")
		}
		return err
	})
}

// runAddUser is the `xy-server adduser <username>` subcommand. The password is
// read from stdin (one line) to keep it out of the process list / shell history.
func runAddUser(args []string) {
	if len(args) < 1 {
		log.Fatal("usage: xy-server adduser <username>  (password on stdin)")
	}
	username := args[0]
	password := os.Getenv("XY_ADMIN_PASSWORD")
	if password == "" {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			log.Fatalf("read password: %v", err)
		}
		password = strings.TrimRight(line, "\r\n")
	}
	srv, err := newServer()
	if err != nil {
		log.Fatal(err)
	}
	if err := srv.addUser(context.Background(), username, password); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("user %q created\n", username)
}
