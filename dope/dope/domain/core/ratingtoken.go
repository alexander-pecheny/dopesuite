package core

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// A user's token for rating.chgk.info (its API's `rt_…`), saved on /profile.
// dope sends it as the user when it exports a fest's rosters and results
// there, so it is stored whole and never shown back.

// RatingToken is the user's saved token for the rating site.
type RatingToken struct {
	Token   string `json:"-"`
	SavedAt string `json:"saved_at"`
}

// Tail is the token's last characters, for telling one token from another.
func (t RatingToken) Tail() string {
	const shown = 4
	if len(t.Token) <= shown {
		return ""
	}
	return t.Token[len(t.Token)-shown:]
}

// LoadRatingToken is the user's token; ok is false when they saved none.
func (e *Engine) LoadRatingToken(ctx context.Context, userID int64) (RatingToken, bool, error) {
	var t RatingToken
	err := e.DB.QueryRowContext(ctx, `select token, saved_at from user_rating_tokens where user_id = ?`, userID).Scan(&t.Token, &t.SavedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, false, nil
	}
	return t, err == nil, err
}

// SaveRatingToken replaces the user's token.
func (e *Engine) SaveRatingToken(ctx context.Context, userID int64, token string) error {
	_, err := e.WriteExec(ctx, `
insert into user_rating_tokens(user_id, token, saved_at) values(?, ?, ?)
on conflict(user_id) do update set token = excluded.token, saved_at = excluded.saved_at`,
		userID, token, time.Now().UTC().Format(time.RFC3339))
	return err
}

// ForgetRatingToken removes the user's token.
func (e *Engine) ForgetRatingToken(ctx context.Context, userID int64) error {
	_, err := e.WriteExec(ctx, `delete from user_rating_tokens where user_id = ?`, userID)
	return err
}
