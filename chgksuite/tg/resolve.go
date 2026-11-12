package tg

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	chgkstrings "code.pecheny.me/pecheny/dopesuite/chgksuite/i18nstrings"
	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"

	_ "modernc.org/sqlite"
)

// A channel is given as a numeric id, a t.me link or an @username. getChat
// takes a public channel's username, and a private channel has none: its id is
// in any link to one of its posts. The discussion group is never asked for:
// the channel names it.

const (
	// Stand-in ids a dry run posts to when it was given names.
	dryRunChannelID = 1111111111
	dryRunChatID    = 2222222222
)

var (
	reChannelLink = regexp.MustCompile(`^https?://t\.me/c/(\d+)`)
	rePublicLink  = regexp.MustCompile(`^https?://t\.me/([^/]+)`)
)

// parseTargetRef reads a channel or chat reference. It returns either an id or a
// username to resolve.
func parseTargetRef(ref string) (id int64, username string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0, "", fmt.Errorf("no channel or chat given")
	}
	if n, convErr := idstr.Parse(ref); convErr == nil {
		if s := strings.TrimPrefix(ref, "-100"); s != ref {
			n, _ = idstr.Parse(s)
		}
		return n, "", nil
	}
	if name, ok := strings.CutPrefix(ref, "@"); ok {
		return 0, name, nil
	}
	if m := reChannelLink.FindStringSubmatch(ref); m != nil {
		n, _ := idstr.Parse(m[1])
		return n, "", nil
	}
	if m := rePublicLink.FindStringSubmatch(ref); m != nil {
		return 0, m[1], nil
	}
	return 0, ref, nil
}

// prefixed is the "-100…" form the Bot API wants for a channel or supergroup.
func prefixed(id int64) string {
	s := idstr.Format(id)
	if strings.HasPrefix(s, "-100") {
		return s
	}
	return "-100" + strings.TrimPrefix(s, "-")
}

// ResolveTarget turns the channel reference into the ids the export posts to:
// the channel's and its discussion group's. The bot must administer both.
func ResolveTarget(ctx context.Context, bot *Bot, channelRef string) (Target, error) {
	var t Target
	channelID, channelName, err := parseTargetRef(channelRef)
	if err != nil {
		return t, fmt.Errorf("channel: %w", err)
	}
	if channelID == 0 {
		channelID = savedID(channelName)
	}
	if channelID == 0 {
		if channelID, err = lookUpUsername(ctx, bot, channelName); err != nil {
			return t, err
		}
	}
	s := chgkstrings.Default
	t.ChannelID = prefixed(channelID)
	if err := verifyAccess(ctx, bot, t.ChannelID, s.Tg.Verify.WhatChannel(), s.Tg.Verify.OfChannel()); err != nil {
		return t, err
	}
	saveID(channelName, t.ChannelID)
	if t.ChatID, err = discussionGroup(ctx, bot, t.ChannelID); err != nil {
		return t, err
	}
	return t, verifyAccess(ctx, bot, t.ChatID, s.Tg.Verify.WhatChat(), s.Tg.Verify.OfChat())
}

// lookUpUsername is the id of the public channel with that username.
func lookUpUsername(ctx context.Context, bot *Bot, name string) (int64, error) {
	res, err := bot.Client().Call(ctx, "getChat", map[string]any{"chat_id": "@" + name})
	if err != nil {
		return 0, corei18n.User(chgkstrings.Default.Tg.Verify.NotFound(name))
	}
	var chat struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(res, &chat); err != nil {
		return 0, err
	}
	return chat.ID, nil
}

// discussionGroup is the group linked to the channel, as getChat reports it.
func discussionGroup(ctx context.Context, bot *Bot, channelID string) (string, error) {
	res, err := bot.Client().Call(ctx, "getChat", map[string]any{"chat_id": channelID})
	if err != nil {
		return "", err
	}
	var chat struct {
		LinkedChatID int64 `json:"linked_chat_id"`
	}
	if err := json.Unmarshal(res, &chat); err != nil {
		return "", err
	}
	if chat.LinkedChatID == 0 {
		return "", corei18n.User(chgkstrings.Default.Tg.Verify.NoDiscussion())
	}
	return prefixed(chat.LinkedChatID), nil
}

// verifyAccess checks the bot is an administrator where it is about to post,
// which is what Telegram requires of it and the commonest thing to have missed.
// The place is named twice because the two failures decline it differently.
func verifyAccess(ctx context.Context, bot *Bot, chatID, dative, genitive string) error {
	res, err := bot.Client().Call(ctx, "getChatAdministrators", map[string]any{"chat_id": chatID})
	if err != nil {
		return corei18n.User(chgkstrings.Default.Tg.Verify.NotMember(dative, err.Error()))
	}
	var admins []struct {
		User struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(res, &admins); err != nil {
		return err
	}
	me, err := bot.Client().Call(ctx, "getMe", map[string]any{})
	if err != nil {
		return err
	}
	var self struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(me, &self); err != nil {
		return err
	}
	for _, a := range admins {
		if a.User.ID == self.ID {
			return nil
		}
	}
	return corei18n.User(chgkstrings.Default.Tg.Verify.NotAdmin(genitive))
}

// savedID is the id chgksuite saved for a name typed before, which need not be
// a username: a private channel's name is the only way to find it again. The
// Python tool keeps them in ~/.chgksuite/resolve.db, which this shares, and
// earlier Go versions in tg-resolve.json, which is still read.
func savedID(name string) int64 {
	dir := chgksuiteDir()
	var cache map[string]int64
	if data, err := os.ReadFile(filepath.Join(dir, "tg-resolve.json")); err == nil {
		_ = json.Unmarshal(data, &cache)
	}
	if id := cache[name]; id != 0 {
		return id
	}
	db, err := openResolveDB(dir)
	if err != nil {
		return 0
	}
	defer db.Close()
	var id int64
	_ = db.QueryRow("SELECT id FROM resolve WHERE username = ? AND id > 0", name).Scan(&id)
	return id
}

// saveID remembers the channel a name resolved to, as chgksuite does: the id
// without its -100.
func saveID(name, channelID string) {
	id, err := strconv.ParseInt(strings.TrimPrefix(channelID, "-100"), 10, 64)
	if name == "" || err != nil {
		return
	}
	db, err := openResolveDB(chgksuiteDir())
	if err != nil {
		return
	}
	defer db.Close()
	_, _ = db.Exec("INSERT OR REPLACE INTO resolve VALUES (?, ?)", name, id)
}

// SavedChannels are the names saved in resolve.db, which the app offers for
// --tgchannel as chgksuite's own window did.
func SavedChannels() []string {
	db, err := openResolveDB(chgksuiteDir())
	if err != nil {
		return nil
	}
	defer db.Close()
	rows, err := db.Query("SELECT username FROM resolve WHERE id > 0 ORDER BY username")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			out = append(out, name)
		}
	}
	return out
}

func openResolveDB(dir string) (*sql.DB, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "resolve.db"))
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS resolve (username TEXT PRIMARY KEY, id INTEGER)"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func chgksuiteDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".chgksuite")
}

// DryRunTarget is where a dry run pretends to post: the channel id it was
// given, or a stand-in for a name, and a stand-in group, since nothing is
// resolved without a bot.
func DryRunTarget(channelRef string) Target {
	channel := prefixed(dryRunChannelID)
	if n, _, err := parseTargetRef(channelRef); err == nil && n != 0 {
		channel = prefixed(n)
	}
	return Target{ChannelID: channel, ChatID: prefixed(dryRunChatID)}
}
